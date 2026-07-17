package server

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	_ "github.com/joho/godotenv/autoload"

	"github.com/shohinx/vanilla-api/internal/database"
	"github.com/shohinx/vanilla-api/internal/dub"
)

type Config struct {
	AdminAPIKey    string
	MenuAppURL     string
	AllowedOrigins []string
	DubAPIKey      string
	DubDomain      string
	DubLinkKey     string
}

type Server struct {
	db     database.Service
	dub    dub.Service
	config Config
}

func New(db database.Service, dubService dub.Service, config Config) *Server {
	return &Server{db: db, dub: dubService, config: config}
}

func NewServer() (*http.Server, error) {
	port, err := strconv.Atoi(envOrDefault("PORT", "8080"))
	if err != nil || port < 1 || port > 65535 {
		return nil, fmt.Errorf("PORT must be a number between 1 and 65535")
	}

	db := database.New()
	if err := db.Initialize(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}

	config := Config{
		AdminAPIKey:    os.Getenv("ADMIN_API_KEY"),
		MenuAppURL:     os.Getenv("MENU_APP_URL"),
		AllowedOrigins: splitCSV(envOrDefault("ALLOWED_ORIGINS", "http://localhost:5173")),
		DubAPIKey:      os.Getenv("DUB_API_KEY"),
		DubDomain:      os.Getenv("DUB_DOMAIN"),
		DubLinkKey:     envOrDefault("DUB_LINK_KEY", "menu"),
	}
	service := New(db, dub.New(config.DubAPIKey), config)
	return &http.Server{
		Addr:         fmt.Sprintf(":%d", port),
		Handler:      service.RegisterRoutes(),
		IdleTimeout:  time.Minute,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
	}, nil
}

func envOrDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func splitCSV(value string) []string {
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}
