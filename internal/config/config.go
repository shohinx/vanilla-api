package config

import (
	"os"
	"strings"
	"time"
)

const (
	defaultEnv             = "development"
	defaultHTTPPort        = "8080"
	defaultDBHost          = "localhost"
	defaultDBPort          = "5432"
	defaultDBUsername      = "postgres"
	defaultDBPassword      = "postgres"
	defaultDBDatabase      = "vanilla_api"
	defaultDBSchema        = "public"
	defaultDBSSLMode       = "disable"
	defaultJWTIssuer       = "vanilla-api"
	defaultAccessTokenTTL  = 15 * time.Minute
	defaultRefreshTokenTTL = 30 * 24 * time.Hour
)

type Config struct {
	Env           string
	HTTP          HTTP
	DB            DB
	Auth          AuthConfig
	Observability Observability
}

type HTTP struct {
	Port           string
	IdleTimeout    time.Duration
	ReadTimeout    time.Duration
	WriteTimeout   time.Duration
	TrustedProxies []string
}

type DB struct {
	DatabaseURL string
	Host        string
	Port        string
	Username    string
	Password    string
	Database    string
	Schema      string
	SSLMode     string
}

type AuthConfig struct {
	JWTSecret       string
	Issuer          string
	AccessTokenTTL  time.Duration
	RefreshTokenTTL time.Duration
}

type Observability struct {
	ServiceName      string
	OTLPHTTPEndpoint string
}

func Load() (Config, error) {
	cfg := Config{
		Env: envOr("APP_ENV", defaultEnv),
		HTTP: HTTP{
			Port: envOr("PORT", defaultHTTPPort),
		},
		DB: DB{
			DatabaseURL: strings.TrimSpace(os.Getenv("DATABASE_URL")),
			Host:        envOr("BLUEPRINT_DB_HOST", defaultDBHost),
			Port:        envOr("BLUEPRINT_DB_PORT", defaultDBPort),
			Username:    envOr("BLUEPRINT_DB_USERNAME", defaultDBUsername),
			Password:    envOr("BLUEPRINT_DB_PASSWORD", defaultDBPassword),
			Database:    envOr("BLUEPRINT_DB_DATABASE", defaultDBDatabase),
			Schema:      envOr("BLUEPRINT_DB_SCHEMA", defaultDBSchema),
			SSLMode:     envOr("BLUEPRINT_DB_SSL_MODE", envOr("BLUEPRINT_DB_SSLMODE", defaultDBSSLMode)),
		},
		Auth: AuthConfig{
			JWTSecret:       envOr("JWT_SECRET", "local-development-secret-change-me"),
			Issuer:          envOr("JWT_ISSUER", defaultJWTIssuer),
			AccessTokenTTL:  defaultAccessTokenTTL,
			RefreshTokenTTL: defaultRefreshTokenTTL,
		},
	}
	return cfg, nil
}

func envOr(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
