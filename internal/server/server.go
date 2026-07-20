package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "github.com/joho/godotenv/autoload"

	"github.com/shohinx/vanilla-api/internal/database"
	"github.com/shohinx/vanilla-api/internal/sdk/models"
	"github.com/shohinx/vanilla-api/internal/service/dub"
	"github.com/shohinx/vanilla-api/internal/service/seaweedfs"
)

const (
	defaultPort         = "8080"
	defaultImageRegion  = "us-east-1"
	defaultAllowOrigins = "http://localhost:5173"
	readHeaderTimeout   = 5 * time.Second
	readTimeout         = 60 * time.Second
	writeTimeout        = 30 * time.Second
	idleTimeout         = 60 * time.Second
	maxHeaderBytes      = 1 << 20
)

type Config struct {
	AdminAPIKey    string
	MenuAppURL     string
	AllowedOrigins []string
	DubAPIKey      string
	DubDomain      string
	DubLinkKey     string
	PublicBaseURL  string
}

// Repository is the persistence contract consumed by the HTTP transport. It
// intentionally excludes database lifecycle methods, keeping handlers easy to
// exercise with small in-memory implementations.
type Repository interface {
	Health(context.Context) (map[string]string, error)
	Menu(context.Context, bool) (models.Menu, error)
	Categories(context.Context) ([]models.MenuCategory, error)
	CreateCategories(context.Context, []models.NewCategory) ([]models.MenuCategory, error)
	UpdateInventory(context.Context, int64, int) (models.Inventory, error)
	UpdateVariantInventory(context.Context, int64, int) (models.VariantInventory, error)
	UpdateItemAvailability(context.Context, int64, bool) (models.ItemAvailability, error)
	UpdateItemImage(context.Context, int64, string) (models.ItemImage, error)
	CreateMenuItems(context.Context, []models.NewItem) ([]models.Item, error)
	CreateOrder(context.Context, models.SubmitOrderRequest, models.Quote) (models.Order, error)
	Orders(context.Context, string) ([]models.Order, error)
	UpdateOrderStatus(context.Context, int64, string, int64) (models.Order, error)
	Staff(context.Context, bool) ([]models.Staff, error)
	StaffActive(context.Context, int64) (bool, error)
	StaffCredentials(context.Context, string) (models.Staff, string, error)
	CreateStaff(context.Context, string, string) (models.Staff, error)
	SetStaffActive(context.Context, int64, bool) (models.Staff, error)
	MenuQR(context.Context) (models.Link, error)
	SaveMenuQR(context.Context, models.Link) error
}

type LinkService interface {
	CreateMenuLink(context.Context, string, string, string) (models.Link, error)
	RetrieveMenuLink(context.Context, string, string, string) (models.Link, error)
	QRCode(context.Context, string) ([]byte, error)
}

type ImageStore interface {
	Put(context.Context, string, io.Reader, int64, string) error
	Get(context.Context, string) (seaweedfs.Object, error)
}

type Server struct {
	db     Repository
	dub    LinkService
	images ImageStore
	config Config
	logger *slog.Logger
}

// Application owns the HTTP server and its process-scoped resources.
type Application struct {
	*http.Server

	closeOnce sync.Once
	closeFunc func() error
	closeErr  error
}

func New(db Repository, dubService LinkService, imageService ImageStore, config Config) *Server {
	return NewWithLogger(db, dubService, imageService, config, slog.Default())
}

func NewWithLogger(
	db Repository,
	dubService LinkService,
	imageService ImageStore,
	config Config,
	logger *slog.Logger,
) *Server {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &Server{db: db, dub: dubService, images: imageService, config: config, logger: logger}
}

func NewServer() (*Application, error) {
	port, err := strconv.Atoi(envOrDefault("PORT", defaultPort))
	if err != nil || port < 1 || port > 65535 {
		return nil, fmt.Errorf("PORT must be a number between 1 and 65535")
	}

	config := Config{
		AdminAPIKey:    os.Getenv("ADMIN_API_KEY"),
		MenuAppURL:     strings.TrimSpace(os.Getenv("MENU_APP_URL")),
		AllowedOrigins: splitCSV(envOrDefault("ALLOWED_ORIGINS", defaultAllowOrigins)),
		DubAPIKey:      strings.TrimSpace(os.Getenv("DUB_API_KEY")),
		DubDomain:      strings.TrimSpace(os.Getenv("DUB_DOMAIN")),
		DubLinkKey:     strings.TrimSpace(os.Getenv("DUB_LINK_KEY")),
		PublicBaseURL:  strings.TrimRight(strings.TrimSpace(os.Getenv("PUBLIC_BASE_URL")), "/"),
	}
	if err := config.validate(); err != nil {
		return nil, err
	}
	imageService, err := seaweedfs.New(context.Background(), seaweedfs.Config{
		Endpoint:  os.Getenv("IMAGE_S3_ENDPOINT"),
		Region:    envOrDefault("IMAGE_S3_REGION", defaultImageRegion),
		AccessKey: os.Getenv("IMAGE_S3_ACCESS_KEY"),
		SecretKey: os.Getenv("IMAGE_S3_SECRET_KEY"),
		Bucket:    os.Getenv("IMAGE_S3_BUCKET"),
	})
	if err != nil {
		return nil, fmt.Errorf("configure image storage: %w", err)
	}

	db, err := database.New()
	if err != nil {
		return nil, fmt.Errorf("configure database: %w", err)
	}
	if err := db.Initialize(context.Background()); err != nil {
		return nil, errors.Join(
			fmt.Errorf("initialize database: %w", err),
			wrapCloseError("database", db.Close()),
		)
	}

	var images ImageStore
	if imageService != nil {
		images = imageService
	}
	service := NewWithLogger(db, dub.New(config.DubAPIKey), images, config, slog.Default())
	httpServer := &http.Server{
		Addr:              fmt.Sprintf(":%d", port),
		Handler:           service.RegisterRoutes(),
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
		MaxHeaderBytes:    maxHeaderBytes,
	}
	return &Application{Server: httpServer, closeFunc: db.Close}, nil
}

func (c Config) validate() error {
	if err := validateOptionalHTTPURL("MENU_APP_URL", c.MenuAppURL); err != nil {
		return err
	}
	if err := validateOptionalHTTPURL("PUBLIC_BASE_URL", c.PublicBaseURL); err != nil {
		return err
	}
	if len(c.AllowedOrigins) != 0 {
		if err := corsConfiguration(c.AllowedOrigins).Validate(); err != nil {
			return fmt.Errorf("validate ALLOWED_ORIGINS: %w", err)
		}
	}

	configuredDubValues := 0
	for _, value := range []string{c.DubAPIKey, c.DubDomain, c.DubLinkKey} {
		if value != "" {
			configuredDubValues++
		}
	}
	if configuredDubValues != 0 && configuredDubValues != 3 {
		return errors.New("DUB_API_KEY, DUB_DOMAIN, and DUB_LINK_KEY must be configured together")
	}
	if c.DubDomain != "" {
		parsed, err := url.Parse("https://" + c.DubDomain)
		if err != nil || parsed.Hostname() == "" || parsed.Port() != "" ||
			parsed.Host != c.DubDomain || parsed.Path != "" {
			return errors.New("DUB_DOMAIN must be a hostname such as dub.sh")
		}
	}
	return nil
}

func validateOptionalHTTPURL(name, value string) error {
	if value == "" {
		return nil
	}
	parsed, err := url.ParseRequestURI(value)
	if err != nil || !parsed.IsAbs() || parsed.Host == "" ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") {
		return fmt.Errorf("%s must be an absolute HTTP(S) URL", name)
	}
	return nil
}

// Shutdown drains active HTTP requests before closing process-scoped
// resources. If graceful draining times out, it force-closes the HTTP server
// before releasing those resources.
func (a *Application) Shutdown(ctx context.Context) error {
	shutdownErr := a.Server.Shutdown(ctx)
	if shutdownErr != nil {
		shutdownErr = errors.Join(shutdownErr, ignoreServerClosed(a.Server.Close()))
	}
	return errors.Join(shutdownErr, a.closeResources())
}

// Close immediately stops HTTP serving and releases process-scoped resources.
func (a *Application) Close() error {
	return errors.Join(ignoreServerClosed(a.Server.Close()), a.closeResources())
}

func (a *Application) closeResources() error {
	a.closeOnce.Do(func() {
		if a.closeFunc != nil {
			a.closeErr = a.closeFunc()
		}
	})
	return wrapCloseError("database", a.closeErr)
}

func ignoreServerClosed(err error) error {
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func envOrDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func splitCSV(value string) []string {
	result := make([]string, 0)
	for part := range strings.SplitSeq(value, ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func wrapCloseError(resource string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("close %s: %w", resource, err)
}
