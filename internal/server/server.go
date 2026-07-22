package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/shohinx/vanilla-api/internal/config"
	"github.com/shohinx/vanilla-api/internal/sdk/models"
	"github.com/shohinx/vanilla-api/internal/sdk/sqldb"
	"github.com/shohinx/vanilla-api/internal/services/dub"
	"github.com/shohinx/vanilla-api/internal/services/jwt"
)

const (
	defaultPort       = "8080"
	readHeaderTimeout = 5 * time.Second
	readTimeout       = 60 * time.Second
	writeTimeout      = 30 * time.Second
	idleTimeout       = 60 * time.Second
	maxHeaderBytes    = 1 << 20
)

type Repository interface {
	Health() map[string]string
	PublicMenuSnapshot(context.Context, string) (models.PublicMenuSnapshot, error)
}

type menuLinkRepository interface {
	RetrieveMenuLinkByKey(context.Context, string, string) (dub.Link, error)
}

type Server struct {
	repository Repository
	db         sqldb.Service
	jwt        jwt.TokenRepository
	menuLinks  menuLinkRepository
	dubDomain  string
	dubLinkKey string
	logger     *slog.Logger
}

func New(repository Repository) *Server {
	server := &Server{repository: repository, logger: slog.Default()}
	if database, ok := repository.(sqldb.Service); ok {
		server.db = database
	}
	return server
}

type Application struct {
	*http.Server

	closeOnce sync.Once
	closeFunc func() error
	closeErr  error
}

func NewServer() (*Application, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("load configuration: %w", err)
	}

	database, err := sqldb.NewService(cfg.DB)
	if err != nil {
		return nil, fmt.Errorf("connect to database: %w", err)
	}

	port := cfg.HTTP.Port
	if port == "" {
		port = defaultPort
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		_ = database.Close()
		return nil, errors.New("HTTP port must be a number between 1 and 65535")
	}

	service := New(database)
	service.jwt = jwt.NewTokenService(cfg.Auth)

	dubConfig := dub.ConfigFromEnv()
	dubConfig.APIKey = strings.TrimSpace(dubConfig.APIKey)
	dubConfig.Domain = strings.TrimSpace(dubConfig.Domain)
	dubConfig.LinkKey = strings.Trim(strings.TrimSpace(dubConfig.LinkKey), "/")
	if dubConfig.APIKey != "" || dubConfig.LinkKey != "" {
		if dubConfig.APIKey == "" || dubConfig.Domain == "" || dubConfig.LinkKey == "" {
			_ = database.Close()
			return nil, errors.New("DUB_API_KEY, DUB_DOMAIN, and DUB_LINK_KEY must be configured together")
		}
		menuLinks, err := dub.New(dubConfig)
		if err != nil {
			_ = database.Close()
			return nil, fmt.Errorf("configure Dub: %w", err)
		}
		service.menuLinks = menuLinks
		service.dubDomain = dubConfig.Domain
		service.dubLinkKey = dubConfig.LinkKey
	}

	httpServer := &http.Server{
		Addr:              ":" + port,
		Handler:           service.RegisterRoutes(),
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       durationOr(cfg.HTTP.ReadTimeout, readTimeout),
		WriteTimeout:      durationOr(cfg.HTTP.WriteTimeout, writeTimeout),
		IdleTimeout:       durationOr(cfg.HTTP.IdleTimeout, idleTimeout),
		MaxHeaderBytes:    maxHeaderBytes,
	}
	return &Application{Server: httpServer, closeFunc: database.Close}, nil
}

func durationOr(value, fallback time.Duration) time.Duration {
	if value > 0 {
		return value
	}
	return fallback
}

func (a *Application) Shutdown(ctx context.Context) error {
	return errors.Join(a.Server.Shutdown(ctx), a.closeResources())
}

func (a *Application) Close() error {
	serverErr := a.Server.Close()
	if errors.Is(serverErr, http.ErrServerClosed) {
		serverErr = nil
	}
	return errors.Join(serverErr, a.closeResources())
}

func (a *Application) closeResources() error {
	a.closeOnce.Do(func() {
		if a.closeFunc != nil {
			a.closeErr = a.closeFunc()
		}
	})
	return a.closeErr
}
