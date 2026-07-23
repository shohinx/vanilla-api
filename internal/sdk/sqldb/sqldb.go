package sqldb

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/shohinx/vanilla-api/internal/config"
	"github.com/shohinx/vanilla-api/internal/sdk/models"
)

// lib/pq errorCodeNames
// https://github.com/lib/pq/blob/master/error.go#L178
const (
	uniqueViolation     = "23505"
	foreignKeyViolation = "23503"
)

var (
	ErrDBNotFound          = sql.ErrNoRows
	ErrDBDuplicatedEntry   = errors.New("duplicated entry")
	ErrForeignKeyViolation = errors.New("foreign key violation")
	ErrForbidden           = errors.New("forbidden")
	ErrConflict            = errors.New("conflict")
	ErrInvalidInput        = errors.New("invalid input")
)

// Service represents a service that interacts with a database.
type Service interface {
	// Health returns a map of health status information.
	// The keys and values in the map are service-specific.
	Health() map[string]string

	// Close terminates the database connection.
	// It returns an error if the connection cannot be closed.
	Close() error

	PublicMenuSnapshot(context.Context, string) (models.PublicMenuSnapshot, error)

	// User operations
	GetUserByID(ctx context.Context, userID string) (models.User, error)
	GetUserByEmail(ctx context.Context, email string) (models.User, error)
	GetUserByUsername(ctx context.Context, username string) (models.User, error)
	CreateUser(ctx context.Context, user models.NewUser) (models.User, error)
	ListUsers(ctx context.Context) ([]models.User, error)
	UpdateUsername(ctx context.Context, userID string, update models.UpdateUsername) (models.User, error)
	PromoteUserToAdmin(ctx context.Context, userID string) (models.User, error)
	DemoteUserFromAdmin(ctx context.Context, userID string) (models.User, error)

	// Refresh token operations
	CreateRefreshToken(ctx context.Context, token models.NewRefreshToken) (models.RefreshToken, error)
	GetRefreshTokenByToken(ctx context.Context, token []byte) (models.RefreshToken, error)
	RotateRefreshToken(ctx context.Context, currentTokenID string, token models.NewRefreshToken) (models.RefreshToken, error)
	RevokeRefreshToken(ctx context.Context, tokenID string) error
	DeleteExpiredRefreshTokens(ctx context.Context) error
	DeleteRefreshTokensByUserID(ctx context.Context, userID string) error

	// Password reset token operations
	CreatePasswordResetToken(ctx context.Context, token models.NewPasswordResetToken) (models.PasswordResetToken, error)
	ConsumePasswordResetToken(ctx context.Context, token string) (models.PasswordResetToken, error)
	ResetPassword(ctx context.Context, token string, newPassword []byte) error
	DeleteExpiredPasswordResetTokens(ctx context.Context) error

	// Password operations
	UpdateUserPassword(ctx context.Context, userID string, newPassword []byte) error
	UpdateUserPasswordAndRevokeTokens(ctx context.Context, userID string, newPassword []byte) error

	// Organization operations
	ListOrganizations(context.Context, models.AdminScope) ([]models.Organization, error)
	CreateOrganization(context.Context, models.OrganizationInput, models.MutationContext) (models.Organization, error)
	GetOrganization(context.Context, string, models.AdminScope) (models.Organization, error)
	UpdateOrganization(context.Context, string, models.OrganizationInput, models.MutationContext) (models.Organization, error)
	ResolveMembership(context.Context, string, string) (models.Membership, error)
	RestaurantBelongsToOrganization(context.Context, string, string) (bool, error)

	// Membership operations
	ListMemberships(context.Context, string) ([]models.Membership, error)
	CreateMembership(context.Context, string, models.MembershipInput, models.MutationContext) (models.Membership, error)
	UpdateMembership(context.Context, string, string, models.MembershipInput, models.MutationContext) (models.Membership, error)
	DeleteMembership(context.Context, string, string, models.MutationContext) error

	// Restaurant operations
	ListRestaurants(context.Context, string) ([]models.Restaurant, error)
	CreateRestaurant(context.Context, string, models.RestaurantInput, models.MutationContext) (models.Restaurant, error)
	GetRestaurant(context.Context, string, string) (models.Restaurant, error)
	UpdateRestaurant(context.Context, string, string, models.RestaurantInput, models.MutationContext) (models.Restaurant, error)
	DeleteRestaurant(context.Context, string, string, models.MutationContext) error

	// Business hour operations
	ListBusinessHours(context.Context, string, string) ([]models.BusinessHour, error)
	CreateBusinessHour(context.Context, string, string, models.BusinessHourInput, models.MutationContext) (models.BusinessHour, error)
	GetBusinessHour(context.Context, string, string, string) (models.BusinessHour, error)
	UpdateBusinessHour(context.Context, string, string, string, models.BusinessHourInput, models.MutationContext) (models.BusinessHour, error)
	DeleteBusinessHour(context.Context, string, string, string, models.MutationContext) error

	// Special hour operations
	ListSpecialHours(context.Context, string, string) ([]models.SpecialHour, error)
	CreateSpecialHour(context.Context, string, string, models.SpecialHourInput, models.MutationContext) (models.SpecialHour, error)
	GetSpecialHour(context.Context, string, string, string) (models.SpecialHour, error)
	UpdateSpecialHour(context.Context, string, string, string, models.SpecialHourInput, models.MutationContext) (models.SpecialHour, error)
	DeleteSpecialHour(context.Context, string, string, string, models.MutationContext) error

	// Catalog item operations
	ListCatalogItems(context.Context, string, string) ([]models.CatalogItem, error)
	CreateCatalogItem(context.Context, string, string, models.CatalogItemInput, models.MutationContext) (models.CatalogItem, error)
	GetCatalogItem(context.Context, string, string, string) (models.CatalogItem, error)
	UpdateCatalogItem(context.Context, string, string, string, models.CatalogItemInput, models.MutationContext) (models.CatalogItem, error)
	DeleteCatalogItem(context.Context, string, string, string, models.MutationContext) error

	// Ingredient operations
	ListIngredients(context.Context, string, string) ([]models.Ingredient, error)
	CreateIngredient(context.Context, string, string, models.IngredientInput, models.MutationContext) (models.Ingredient, error)
	GetIngredient(context.Context, string, string, string) (models.Ingredient, error)
	UpdateIngredient(context.Context, string, string, string, models.IngredientInput, models.MutationContext) (models.Ingredient, error)
	DeleteIngredient(context.Context, string, string, string, models.MutationContext) error

	// Menu operations
	ListMenus(context.Context, string, string) ([]models.Menu, error)
	CreateMenu(context.Context, string, string, models.MenuInput, models.MutationContext) (models.Menu, error)
	GetMenu(context.Context, string, string, string) (models.Menu, error)
	UpdateMenu(context.Context, string, string, string, models.MenuInput, models.MutationContext) (models.Menu, error)
	DeleteMenu(context.Context, string, string, string, models.MutationContext) error

	// Menu schedule operations
	ListMenuSchedules(context.Context, string, string, string) ([]models.MenuSchedule, error)
	CreateMenuSchedule(context.Context, string, string, string, models.MenuScheduleInput, models.MutationContext) (models.MenuSchedule, error)
	GetMenuSchedule(context.Context, string, string, string) (models.MenuSchedule, error)
	UpdateMenuSchedule(context.Context, string, string, string, models.MenuScheduleInput, models.MutationContext) (models.MenuSchedule, error)
	DeleteMenuSchedule(context.Context, string, string, string, models.MutationContext) error

	// Menu section operations
	ListMenuSections(context.Context, string, string, string) ([]models.MenuSection, error)
	CreateMenuSection(context.Context, string, string, string, models.MenuSectionInput, models.MutationContext) (models.MenuSection, error)
	GetMenuSection(context.Context, string, string, string, string) (models.MenuSection, error)
	UpdateMenuSection(context.Context, string, string, string, string, models.MenuSectionInput, models.MutationContext) (models.MenuSection, error)
	DeleteMenuSection(context.Context, string, string, string, string, models.MutationContext) error

	// Menu entry operations
	ListMenuEntries(context.Context, string, string, string, string) ([]models.MenuEntry, error)
	CreateMenuEntry(context.Context, string, string, string, string, models.MenuEntryInput, models.MutationContext) (models.MenuEntry, error)
	GetMenuEntry(context.Context, string, string, string, string, string) (models.MenuEntry, error)
	UpdateMenuEntry(context.Context, string, string, string, string, string, models.MenuEntryInput, models.MutationContext) (models.MenuEntry, error)
	DeleteMenuEntry(context.Context, string, string, string, string, string, models.MutationContext) error

	// Daily special operations
	ListDailySpecials(context.Context, string, string) ([]models.DailySpecial, error)
	CreateDailySpecial(context.Context, string, string, models.DailySpecialInput, models.MutationContext) (models.DailySpecial, error)
	GetDailySpecial(context.Context, string, string, string) (models.DailySpecial, error)
	UpdateDailySpecial(context.Context, string, string, string, models.DailySpecialInput, models.MutationContext) (models.DailySpecial, error)
	DeleteDailySpecial(context.Context, string, string, string, models.MutationContext) error

	// Allergen operations
	ListAllergens(context.Context, string) ([]models.Allergen, error)
	CreateAllergen(context.Context, string, models.AllergenInput, models.MutationContext) (models.Allergen, error)
	GetAllergen(context.Context, string, string) (models.Allergen, error)
	UpdateAllergen(context.Context, string, string, models.AllergenInput, models.MutationContext) (models.Allergen, error)
	DeleteAllergen(context.Context, string, string, models.MutationContext) error
}

type service struct {
	db       *sql.DB
	database string
}

func NewService(cfg config.DB) (Service, error) {
	connectionURL, err := databaseConnectionURL(cfg)
	if err != nil {
		return nil, err
	}

	databaseName := strings.TrimPrefix(connectionURL.Path, "/")
	if databaseName == "" {
		databaseName = cfg.Database
	}

	db, err := sql.Open("pgx", connectionURL.String())
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}

	// Bound the pool so a traffic spike queues here instead of exhausting
	// Postgres connections.
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(25)
	db.SetConnMaxLifetime(5 * time.Minute)
	db.SetConnMaxIdleTime(5 * time.Minute)

	// Fail fast on bad credentials or an unreachable host rather than
	// surfacing the problem on the first query.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("pinging database %q at %s: %w", databaseName, connectionURL.Host, err)
	}

	return &service{
		db:       db,
		database: databaseName,
	}, nil
}

func databaseConnectionURL(cfg config.DB) (*url.URL, error) {
	if cfg.DatabaseURL != "" {
		connectionURL, err := url.Parse(cfg.DatabaseURL)
		if err != nil || connectionURL.Host == "" || (connectionURL.Scheme != "postgres" && connectionURL.Scheme != "postgresql") {
			return nil, errors.New("DATABASE_URL must be a valid postgres or postgresql URL")
		}
		query := connectionURL.Query()
		if !query.Has("sslmode") && cfg.SSLMode != "" {
			query.Set("sslmode", cfg.SSLMode)
		}
		if !query.Has("search_path") && cfg.Schema != "" {
			query.Set("search_path", cfg.Schema)
		}
		connectionURL.RawQuery = query.Encode()
		return connectionURL, nil
	}

	connectionURL := &url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(cfg.Username, cfg.Password),
		Host:   net.JoinHostPort(cfg.Host, cfg.Port),
		Path:   cfg.Database,
	}
	query := connectionURL.Query()
	query.Set("sslmode", cfg.SSLMode)
	query.Set("search_path", cfg.Schema)
	connectionURL.RawQuery = query.Encode()
	return connectionURL, nil
}

// Health checks the health of the database connection by pinging the database.
// It returns a map with keys indicating various health statistics.
func (s *service) Health() map[string]string {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	stats := make(map[string]string)

	// Ping the database
	err := s.db.PingContext(ctx)
	if err != nil {
		stats["status"] = "down"
		stats["error"] = "database unavailable"
		log.Printf("db down: %v", err)
		return stats
	}

	// Database is up, add more statistics
	stats["status"] = "up"
	stats["message"] = "It's healthy"

	// Get database stats (like open connections, in use, idle, etc.)
	dbStats := s.db.Stats()
	stats["open_connections"] = strconv.Itoa(dbStats.OpenConnections)
	stats["in_use"] = strconv.Itoa(dbStats.InUse)
	stats["idle"] = strconv.Itoa(dbStats.Idle)
	stats["wait_count"] = strconv.FormatInt(dbStats.WaitCount, 10)
	stats["wait_duration"] = dbStats.WaitDuration.String()
	stats["max_idle_closed"] = strconv.FormatInt(dbStats.MaxIdleClosed, 10)
	stats["max_lifetime_closed"] = strconv.FormatInt(dbStats.MaxLifetimeClosed, 10)

	return stats
}

// Close closes the database connection.
// It logs a message indicating the disconnection from the specific database.
// If the connection is successfully closed, it returns nil.
// If an error occurs while closing the connection, it returns the error.
func (s *service) Close() error {
	if err := s.db.Close(); err != nil {
		return fmt.Errorf("closing database %q: %w", s.database, err)
	}
	log.Printf("disconnected from database: %s", s.database)
	return nil
}

func (s *service) PublicMenuSnapshot(ctx context.Context, restaurantSlug string) (models.PublicMenuSnapshot, error) {
	restaurantSlug = strings.TrimSpace(restaurantSlug)
	if restaurantSlug == "" {
		return models.PublicMenuSnapshot{}, errors.New("restaurant slug is required")
	}
	var snapshot models.PublicMenuSnapshot
	err := s.db.QueryRowContext(ctx, `
		SELECT payload_jsonb, etag, generated_at
		FROM get_public_menu_snapshot($1)`, restaurantSlug,
	).Scan(&snapshot.Payload, &snapshot.ETag, &snapshot.GeneratedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return models.PublicMenuSnapshot{}, ErrDBNotFound
	}
	if err != nil {
		return models.PublicMenuSnapshot{}, fmt.Errorf("load public menu snapshot: %w", err)
	}
	return snapshot, nil
}

const userColumns = `
	id, name, username, email, password, avatar_photo_id, role,
	created_at, updated_at`

type rowScanner interface {
	Scan(...any) error
}

type rowQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func scanUser(row rowScanner) (models.User, error) {
	var user models.User
	err := row.Scan(
		&user.ID,
		&user.Name,
		&user.Username,
		&user.Email,
		&user.Password,
		&user.AvatarPhotoID,
		&user.Role,
		&user.CreatedAt,
		&user.UpdatedAt,
	)
	if err != nil {
		return models.User{}, mapDBError(err)
	}
	return user, nil
}

func mapDBError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return ErrDBNotFound
	}
	var pgError *pgconn.PgError
	if errors.As(err, &pgError) {
		switch pgError.Code {
		case uniqueViolation:
			return fmt.Errorf("%w: %w", ErrDBDuplicatedEntry, err)
		case foreignKeyViolation:
			return fmt.Errorf("%w: %w", ErrForeignKeyViolation, err)
		}
	}
	return err
}

func (s *service) GetUserByID(ctx context.Context, userID string) (models.User, error) {
	return scanUser(s.db.QueryRowContext(ctx, `SELECT `+userColumns+` FROM users WHERE id = $1`, userID))
}

func (s *service) GetUserByEmail(ctx context.Context, email string) (models.User, error) {
	return scanUser(s.db.QueryRowContext(ctx, `SELECT `+userColumns+` FROM users WHERE LOWER(email) = LOWER($1)`, strings.TrimSpace(email)))
}

func (s *service) GetUserByUsername(ctx context.Context, username string) (models.User, error) {
	return scanUser(s.db.QueryRowContext(ctx, `SELECT `+userColumns+` FROM users WHERE LOWER(username) = LOWER($1)`, strings.TrimSpace(username)))
}

func (s *service) CreateUser(ctx context.Context, newUser models.NewUser) (models.User, error) {
	return scanUser(s.db.QueryRowContext(ctx, `
		INSERT INTO users (name, username, email, password, role)
		VALUES ($1, $2, $3, $4, 'user')
		RETURNING `+userColumns,
		strings.TrimSpace(newUser.Name),
		strings.TrimSpace(newUser.Username),
		strings.ToLower(strings.TrimSpace(newUser.Email)),
		newUser.Password,
	))
}

func (s *service) ListUsers(ctx context.Context) ([]models.User, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+userColumns+` FROM users ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", mapDBError(err))
	}
	defer rows.Close()

	users := make([]models.User, 0)
	for rows.Next() {
		user, err := scanUser(rows)
		if err != nil {
			return nil, fmt.Errorf("scan user: %w", err)
		}
		users = append(users, user)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate users: %w", err)
	}
	return users, nil
}

func (s *service) UpdateUsername(ctx context.Context, userID string, update models.UpdateUsername) (models.User, error) {
	update.Username = strings.TrimSpace(update.Username)
	if update.Username == "" {
		return models.User{}, ErrInvalidInput
	}
	return scanUser(s.db.QueryRowContext(ctx, `
		UPDATE users
		SET username = $2, updated_at = NOW()
		WHERE id = $1
		RETURNING `+userColumns,
		userID,
		update.Username,
	))
}

func (s *service) PromoteUserToAdmin(ctx context.Context, userID string) (models.User, error) {
	return s.setUserRole(ctx, userID, models.RoleAdmin)
}

func (s *service) DemoteUserFromAdmin(ctx context.Context, userID string) (models.User, error) {
	return s.setUserRole(ctx, userID, models.RoleUser)
}

func (s *service) setUserRole(ctx context.Context, userID string, role models.Role) (models.User, error) {
	return scanUser(s.db.QueryRowContext(ctx, `
		UPDATE users
		SET role = $2, updated_at = NOW()
		WHERE id = $1
		RETURNING `+userColumns,
		userID, role,
	))
}

func scanRefreshToken(row rowScanner) (models.RefreshToken, error) {
	var token models.RefreshToken
	err := row.Scan(
		&token.ID,
		&token.UserID,
		&token.Token,
		&token.ExpiresAt,
		&token.RevokedAt,
		&token.CreatedAt,
		&token.UpdatedAt,
	)
	if err != nil {
		return models.RefreshToken{}, mapDBError(err)
	}
	return token, nil
}

func (s *service) CreateRefreshToken(ctx context.Context, newToken models.NewRefreshToken) (models.RefreshToken, error) {
	return scanRefreshToken(s.db.QueryRowContext(ctx, `
		INSERT INTO refresh_tokens (user_id, token, expires_at)
		VALUES ($1, $2, $3)
		RETURNING id, user_id, token, expires_at, revoked_at, created_at, updated_at`,
		newToken.UserID, newToken.Token, newToken.ExpiresAt,
	))
}

func (s *service) GetRefreshTokenByToken(ctx context.Context, value []byte) (models.RefreshToken, error) {
	return scanRefreshToken(s.db.QueryRowContext(ctx, `
		SELECT id, user_id, token, expires_at, revoked_at, created_at, updated_at
		FROM refresh_tokens
		WHERE token = $1`, value,
	))
}

func (s *service) RotateRefreshToken(ctx context.Context, currentTokenID string, newToken models.NewRefreshToken) (models.RefreshToken, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return models.RefreshToken{}, fmt.Errorf("begin refresh token rotation: %w", err)
	}
	defer tx.Rollback()

	var userID string
	err = tx.QueryRowContext(ctx, `
		UPDATE refresh_tokens
		SET revoked_at = NOW(), updated_at = NOW()
		WHERE id = $1 AND revoked_at IS NULL AND expires_at > NOW()
		RETURNING user_id`, currentTokenID,
	).Scan(&userID)
	if err != nil {
		return models.RefreshToken{}, mapDBError(err)
	}
	if userID != newToken.UserID {
		return models.RefreshToken{}, ErrForeignKeyViolation
	}

	rotated, err := scanRefreshToken(tx.QueryRowContext(ctx, `
		INSERT INTO refresh_tokens (user_id, token, expires_at)
		VALUES ($1, $2, $3)
		RETURNING id, user_id, token, expires_at, revoked_at, created_at, updated_at`,
		newToken.UserID, newToken.Token, newToken.ExpiresAt,
	))
	if err != nil {
		return models.RefreshToken{}, err
	}
	if err := tx.Commit(); err != nil {
		return models.RefreshToken{}, fmt.Errorf("commit refresh token rotation: %w", err)
	}
	return rotated, nil
}

func (s *service) RevokeRefreshToken(ctx context.Context, tokenID string) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE refresh_tokens
		SET revoked_at = COALESCE(revoked_at, NOW()), updated_at = NOW()
		WHERE id = $1`, tokenID,
	)
	return requireAffectedRow(result, err)
}

func (s *service) DeleteExpiredRefreshTokens(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM refresh_tokens WHERE expires_at <= NOW()`)
	if err != nil {
		return fmt.Errorf("delete expired refresh tokens: %w", mapDBError(err))
	}
	return nil
}

func (s *service) DeleteRefreshTokensByUserID(ctx context.Context, userID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM refresh_tokens WHERE user_id = $1`, userID)
	if err != nil {
		return fmt.Errorf("delete user refresh tokens: %w", mapDBError(err))
	}
	return nil
}

func scanPasswordResetToken(row rowScanner) (models.PasswordResetToken, error) {
	var token models.PasswordResetToken
	err := row.Scan(&token.ID, &token.UserID, &token.Token, &token.ExpiresAt, &token.UsedAt, &token.CreatedAt)
	if err != nil {
		return models.PasswordResetToken{}, mapDBError(err)
	}
	return token, nil
}

func (s *service) CreatePasswordResetToken(ctx context.Context, newToken models.NewPasswordResetToken) (models.PasswordResetToken, error) {
	return scanPasswordResetToken(s.db.QueryRowContext(ctx, `
		INSERT INTO password_reset_tokens (user_id, token, expires_at)
		VALUES ($1, $2, $3)
		RETURNING id, user_id, token, expires_at, used_at, created_at`,
		newToken.UserID, newToken.Token, newToken.ExpiresAt,
	))
}

func (s *service) ConsumePasswordResetToken(ctx context.Context, value string) (models.PasswordResetToken, error) {
	return scanPasswordResetToken(s.db.QueryRowContext(ctx, `
		UPDATE password_reset_tokens
		SET used_at = NOW()
		WHERE token = $1 AND used_at IS NULL AND expires_at > NOW()
		RETURNING id, user_id, token, expires_at, used_at, created_at`, value,
	))
}

func (s *service) ResetPassword(ctx context.Context, tokenValue string, newPassword []byte) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin password reset: %w", err)
	}
	defer tx.Rollback()

	var userID string
	err = tx.QueryRowContext(ctx, `
		UPDATE password_reset_tokens
		SET used_at = NOW()
		WHERE token = $1 AND used_at IS NULL AND expires_at > NOW()
		RETURNING user_id`, tokenValue,
	).Scan(&userID)
	if err != nil {
		return mapDBError(err)
	}
	if result, err := tx.ExecContext(ctx, `
		UPDATE users SET password = $2, updated_at = NOW() WHERE id = $1`, userID, newPassword,
	); err != nil {
		return mapDBError(err)
	} else if err := affectedRow(result); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM refresh_tokens WHERE user_id = $1`, userID); err != nil {
		return mapDBError(err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit password reset: %w", err)
	}
	return nil
}

func (s *service) DeleteExpiredPasswordResetTokens(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM password_reset_tokens WHERE expires_at <= NOW() OR used_at IS NOT NULL`)
	if err != nil {
		return fmt.Errorf("delete expired password reset tokens: %w", mapDBError(err))
	}
	return nil
}

func (s *service) UpdateUserPassword(ctx context.Context, userID string, newPassword []byte) error {
	result, err := s.db.ExecContext(ctx, `UPDATE users SET password = $2, updated_at = NOW() WHERE id = $1`, userID, newPassword)
	return requireAffectedRow(result, err)
}

func (s *service) UpdateUserPasswordAndRevokeTokens(ctx context.Context, userID string, newPassword []byte) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin password update: %w", err)
	}
	defer tx.Rollback()

	result, err := tx.ExecContext(ctx, `UPDATE users SET password = $2, updated_at = NOW() WHERE id = $1`, userID, newPassword)
	if err != nil {
		return mapDBError(err)
	}
	if err := affectedRow(result); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM refresh_tokens WHERE user_id = $1`, userID); err != nil {
		return mapDBError(err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit password update: %w", err)
	}
	return nil
}

func requireAffectedRow(result sql.Result, err error) error {
	if err != nil {
		return mapDBError(err)
	}
	return affectedRow(result)
}

func affectedRow(result sql.Result) error {
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrDBNotFound
	}
	return nil
}

func scanOrganization(row rowScanner) (models.Organization, error) {
	var organization models.Organization
	if err := row.Scan(
		&organization.ID,
		&organization.Name,
		&organization.Slug,
		&organization.CreatedAt,
		&organization.UpdatedAt,
	); err != nil {
		return models.Organization{}, mapDBError(err)
	}
	return organization, nil
}

func (s *service) ListOrganizations(ctx context.Context, scope models.AdminScope) ([]models.Organization, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, slug, created_at, updated_at
		FROM organizations
		WHERE $1 = '' OR id::text = $1
		ORDER BY name`, scope.OrganizationID,
	)
	if err != nil {
		return nil, fmt.Errorf("list organizations: %w", mapDBError(err))
	}
	defer rows.Close()
	organizations := make([]models.Organization, 0)
	for rows.Next() {
		organization, err := scanOrganization(rows)
		if err != nil {
			return nil, err
		}
		organizations = append(organizations, organization)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate organizations: %w", err)
	}
	return organizations, nil
}

func (s *service) CreateOrganization(
	ctx context.Context,
	input models.OrganizationInput,
	_ models.MutationContext,
) (models.Organization, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.Slug = strings.TrimSpace(input.Slug)
	if input.Name == "" || input.Slug == "" {
		return models.Organization{}, ErrInvalidInput
	}
	return scanOrganization(s.db.QueryRowContext(ctx, `
		INSERT INTO organizations (name, slug)
		VALUES ($1, $2)
		RETURNING id, name, slug, created_at, updated_at`, input.Name, input.Slug,
	))
}

func (s *service) GetOrganization(ctx context.Context, organizationID string, scope models.AdminScope) (models.Organization, error) {
	return scanOrganization(s.db.QueryRowContext(ctx, `
		SELECT id, name, slug, created_at, updated_at
		FROM organizations
		WHERE id = $1 AND ($2 = '' OR id::text = $2)`, organizationID, scope.OrganizationID,
	))
}

func (s *service) UpdateOrganization(
	ctx context.Context,
	organizationID string,
	input models.OrganizationInput,
	_ models.MutationContext,
) (models.Organization, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.Slug = strings.TrimSpace(input.Slug)
	if input.Name == "" || input.Slug == "" {
		return models.Organization{}, ErrInvalidInput
	}
	return scanOrganization(s.db.QueryRowContext(ctx, `
		UPDATE organizations
		SET name = $2, slug = $3, updated_at = NOW()
		WHERE id = $1
		RETURNING id, name, slug, created_at, updated_at`, organizationID, input.Name, input.Slug,
	))
}

func scanMembership(row rowScanner) (models.Membership, error) {
	var membership models.Membership
	if err := row.Scan(&membership.UserID, &membership.OrganizationID, &membership.Role, &membership.Status); err != nil {
		return models.Membership{}, mapDBError(err)
	}
	return membership, nil
}

func (s *service) ResolveMembership(ctx context.Context, authSubject, organizationID string) (models.Membership, error) {
	return scanMembership(s.db.QueryRowContext(ctx, `
		SELECT user_id, organization_id, role, status
		FROM memberships
		WHERE user_id = $1 AND organization_id = $2 AND status = 'active'`, authSubject, organizationID,
	))
}

func (s *service) RestaurantBelongsToOrganization(ctx context.Context, restaurantID, organizationID string) (bool, error) {
	var belongs bool
	err := s.db.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM restaurants WHERE id = $1 AND organization_id = $2
		)`, restaurantID, organizationID,
	).Scan(&belongs)
	if err != nil {
		return false, fmt.Errorf("check restaurant organization: %w", mapDBError(err))
	}
	return belongs, nil
}

func (s *service) ListMemberships(ctx context.Context, organizationID string) ([]models.Membership, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT user_id, organization_id, role, status
		FROM memberships
		WHERE organization_id = $1
		ORDER BY user_id`, organizationID,
	)
	if err != nil {
		return nil, fmt.Errorf("list memberships: %w", mapDBError(err))
	}
	defer rows.Close()
	memberships := make([]models.Membership, 0)
	for rows.Next() {
		membership, err := scanMembership(rows)
		if err != nil {
			return nil, err
		}
		memberships = append(memberships, membership)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate memberships: %w", err)
	}
	return memberships, nil
}

func (s *service) CreateMembership(
	ctx context.Context,
	organizationID string,
	input models.MembershipInput,
	_ models.MutationContext,
) (models.Membership, error) {
	return scanMembership(s.db.QueryRowContext(ctx, `
		INSERT INTO memberships (organization_id, user_id, role, status)
		SELECT $1, id, $4, $5
		FROM users
		WHERE ($2 <> '' AND id = $2) OR ($3 <> '' AND LOWER(email) = LOWER($3))
		RETURNING user_id, organization_id, role, status`,
		organizationID, input.AuthSubject, input.Email, input.Role, input.Status,
	))
}

func (s *service) UpdateMembership(
	ctx context.Context,
	organizationID, userID string,
	input models.MembershipInput,
	_ models.MutationContext,
) (models.Membership, error) {
	return scanMembership(s.db.QueryRowContext(ctx, `
		UPDATE memberships
		SET role = $3, status = $4
		WHERE organization_id = $1 AND user_id = $2
		RETURNING user_id, organization_id, role, status`,
		organizationID, userID, input.Role, input.Status,
	))
}

func (s *service) DeleteMembership(ctx context.Context, organizationID, userID string, _ models.MutationContext) error {
	result, err := s.db.ExecContext(ctx, `
		DELETE FROM memberships WHERE organization_id = $1 AND user_id = $2`, organizationID, userID,
	)
	return requireAffectedRow(result, err)
}

func scanRestaurant(row rowScanner) (models.Restaurant, error) {
	var restaurant models.Restaurant
	if err := row.Scan(
		&restaurant.ID,
		&restaurant.OrganizationID,
		&restaurant.Name,
		&restaurant.Slug,
		&restaurant.Timezone,
		&restaurant.Currency,
		&restaurant.Status,
	); err != nil {
		return models.Restaurant{}, mapDBError(err)
	}
	return restaurant, nil
}

func (s *service) ListRestaurants(ctx context.Context, organizationID string) ([]models.Restaurant, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, organization_id, name, slug, timezone, currency, status
		FROM restaurants
		WHERE organization_id = $1
		ORDER BY name`, organizationID,
	)
	if err != nil {
		return nil, fmt.Errorf("list restaurants: %w", mapDBError(err))
	}
	defer rows.Close()
	restaurants := make([]models.Restaurant, 0)
	for rows.Next() {
		restaurant, err := scanRestaurant(rows)
		if err != nil {
			return nil, err
		}
		restaurants = append(restaurants, restaurant)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate restaurants: %w", err)
	}
	return restaurants, nil
}

func (s *service) CreateRestaurant(
	ctx context.Context,
	organizationID string,
	input models.RestaurantInput,
	_ models.MutationContext,
) (models.Restaurant, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.Slug = strings.TrimSpace(input.Slug)
	input.Timezone = strings.TrimSpace(input.Timezone)
	input.Currency = strings.TrimSpace(input.Currency)
	input.Status = strings.TrimSpace(input.Status)
	if organizationID == "" || input.Name == "" || input.Slug == "" || input.Timezone == "" || input.Currency == "" || input.Status == "" {
		return models.Restaurant{}, ErrInvalidInput
	}
	return scanRestaurant(s.db.QueryRowContext(ctx, `
		INSERT INTO restaurants (organization_id, name, slug, timezone, currency, status)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, organization_id, name, slug, timezone, currency, status`,
		organizationID, input.Name, input.Slug, input.Timezone, input.Currency, input.Status,
	))
}

func (s *service) GetRestaurant(ctx context.Context, organizationID, restaurantID string) (models.Restaurant, error) {
	return scanRestaurant(s.db.QueryRowContext(ctx, `
		SELECT id, organization_id, name, slug, timezone, currency, status
		FROM restaurants
		WHERE organization_id = $1 AND id = $2`, organizationID, restaurantID,
	))
}

func (s *service) UpdateRestaurant(
	ctx context.Context,
	organizationID, restaurantID string,
	input models.RestaurantInput,
	_ models.MutationContext,
) (models.Restaurant, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.Slug = strings.TrimSpace(input.Slug)
	input.Timezone = strings.TrimSpace(input.Timezone)
	input.Currency = strings.TrimSpace(input.Currency)
	input.Status = strings.TrimSpace(input.Status)
	if input.Name == "" || input.Slug == "" || input.Timezone == "" || input.Currency == "" || input.Status == "" {
		return models.Restaurant{}, ErrInvalidInput
	}
	return scanRestaurant(s.db.QueryRowContext(ctx, `
		UPDATE restaurants
		SET name = $3, slug = $4, timezone = $5, currency = $6, status = $7
		WHERE organization_id = $1 AND id = $2
		RETURNING id, organization_id, name, slug, timezone, currency, status`,
		organizationID, restaurantID, input.Name, input.Slug, input.Timezone, input.Currency, input.Status,
	))
}

func (s *service) DeleteRestaurant(ctx context.Context, organizationID, restaurantID string, _ models.MutationContext) error {
	result, err := s.db.ExecContext(ctx, `
		DELETE FROM restaurants WHERE organization_id = $1 AND id = $2`, organizationID, restaurantID,
	)
	return requireAffectedRow(result, err)
}

func scanBusinessHour(row rowScanner) (models.BusinessHour, error) {
	var businessHour models.BusinessHour
	if err := row.Scan(
		&businessHour.ID,
		&businessHour.RestaurantID,
		&businessHour.DayOfWeek,
		&businessHour.OpensAt,
		&businessHour.ClosesAt,
	); err != nil {
		return models.BusinessHour{}, mapDBError(err)
	}
	return businessHour, nil
}

func (s *service) ListBusinessHours(ctx context.Context, organizationID, restaurantID string) ([]models.BusinessHour, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT bh.id, bh.restaurant_id, bh.day_of_week, bh.opens_at::text, bh.closes_at::text
		FROM business_hours bh
		JOIN restaurants r ON r.id = bh.restaurant_id
		WHERE r.organization_id = $1 AND bh.restaurant_id = $2
		ORDER BY bh.day_of_week, bh.opens_at`, organizationID, restaurantID,
	)
	if err != nil {
		return nil, fmt.Errorf("list business hours: %w", mapDBError(err))
	}
	defer rows.Close()
	businessHours := make([]models.BusinessHour, 0)
	for rows.Next() {
		businessHour, err := scanBusinessHour(rows)
		if err != nil {
			return nil, err
		}
		businessHours = append(businessHours, businessHour)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate business hours: %w", err)
	}
	return businessHours, nil
}

func (s *service) CreateBusinessHour(
	ctx context.Context,
	organizationID, restaurantID string,
	input models.BusinessHourInput,
	_ models.MutationContext,
) (models.BusinessHour, error) {
	input, err := normalizeBusinessHourInput(input)
	if err != nil {
		return models.BusinessHour{}, err
	}
	return scanBusinessHour(s.db.QueryRowContext(ctx, `
		INSERT INTO business_hours (restaurant_id, day_of_week, opens_at, closes_at)
		SELECT r.id, $3, $4::time, $5::time
		FROM restaurants r
		WHERE r.organization_id = $1 AND r.id = $2
		RETURNING id, restaurant_id, day_of_week, opens_at::text, closes_at::text`,
		organizationID, restaurantID, input.DayOfWeek, input.OpensAt, input.ClosesAt,
	))
}

func (s *service) GetBusinessHour(ctx context.Context, organizationID, restaurantID, businessHourID string) (models.BusinessHour, error) {
	return scanBusinessHour(s.db.QueryRowContext(ctx, `
		SELECT bh.id, bh.restaurant_id, bh.day_of_week, bh.opens_at::text, bh.closes_at::text
		FROM business_hours bh
		JOIN restaurants r ON r.id = bh.restaurant_id
		WHERE r.organization_id = $1 AND bh.restaurant_id = $2 AND bh.id = $3`,
		organizationID, restaurantID, businessHourID,
	))
}

func (s *service) UpdateBusinessHour(
	ctx context.Context,
	organizationID, restaurantID, businessHourID string,
	input models.BusinessHourInput,
	_ models.MutationContext,
) (models.BusinessHour, error) {
	input, err := normalizeBusinessHourInput(input)
	if err != nil {
		return models.BusinessHour{}, err
	}
	return scanBusinessHour(s.db.QueryRowContext(ctx, `
		UPDATE business_hours bh
		SET day_of_week = $4, opens_at = $5::time, closes_at = $6::time
		FROM restaurants r
		WHERE bh.id = $3 AND bh.restaurant_id = $2
		  AND r.id = bh.restaurant_id AND r.organization_id = $1
		RETURNING bh.id, bh.restaurant_id, bh.day_of_week, bh.opens_at::text, bh.closes_at::text`,
		organizationID, restaurantID, businessHourID, input.DayOfWeek, input.OpensAt, input.ClosesAt,
	))
}

func (s *service) DeleteBusinessHour(
	ctx context.Context,
	organizationID, restaurantID, businessHourID string,
	_ models.MutationContext,
) error {
	result, err := s.db.ExecContext(ctx, `
		DELETE FROM business_hours bh
		USING restaurants r
		WHERE bh.id = $3 AND bh.restaurant_id = $2
		  AND r.id = bh.restaurant_id AND r.organization_id = $1`,
		organizationID, restaurantID, businessHourID,
	)
	return requireAffectedRow(result, err)
}

func normalizeBusinessHourInput(input models.BusinessHourInput) (models.BusinessHourInput, error) {
	if input.DayOfWeek < 0 || input.DayOfWeek > 6 {
		return models.BusinessHourInput{}, ErrInvalidInput
	}
	opensAt, closesAt, err := normalizeClockRange(input.OpensAt, input.ClosesAt)
	if err != nil {
		return models.BusinessHourInput{}, err
	}
	input.OpensAt = opensAt
	input.ClosesAt = closesAt
	return input, nil
}

func scanSpecialHour(row rowScanner) (models.SpecialHour, error) {
	var specialHour models.SpecialHour
	if err := row.Scan(
		&specialHour.ID,
		&specialHour.RestaurantID,
		&specialHour.Date,
		&specialHour.OpensAt,
		&specialHour.ClosesAt,
		&specialHour.IsClosed,
		&specialHour.Note,
	); err != nil {
		return models.SpecialHour{}, mapDBError(err)
	}
	return specialHour, nil
}

func (s *service) ListSpecialHours(ctx context.Context, organizationID, restaurantID string) ([]models.SpecialHour, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT sh.id, sh.restaurant_id, sh.special_date::text,
		       COALESCE(sh.opens_at::text, ''), COALESCE(sh.closes_at::text, ''),
		       sh.is_closed, sh.note
		FROM special_hours sh
		JOIN restaurants r ON r.id = sh.restaurant_id
		WHERE r.organization_id = $1 AND sh.restaurant_id = $2
		ORDER BY sh.special_date`, organizationID, restaurantID,
	)
	if err != nil {
		return nil, fmt.Errorf("list special hours: %w", mapDBError(err))
	}
	defer rows.Close()
	specialHours := make([]models.SpecialHour, 0)
	for rows.Next() {
		specialHour, err := scanSpecialHour(rows)
		if err != nil {
			return nil, err
		}
		specialHours = append(specialHours, specialHour)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate special hours: %w", err)
	}
	return specialHours, nil
}

func (s *service) CreateSpecialHour(
	ctx context.Context,
	organizationID, restaurantID string,
	input models.SpecialHourInput,
	_ models.MutationContext,
) (models.SpecialHour, error) {
	input, err := normalizeSpecialHourInput(input)
	if err != nil {
		return models.SpecialHour{}, err
	}
	return scanSpecialHour(s.db.QueryRowContext(ctx, `
		INSERT INTO special_hours (restaurant_id, special_date, opens_at, closes_at, is_closed, note)
		SELECT r.id, $3::date, NULLIF($4, '')::time, NULLIF($5, '')::time, $6, $7
		FROM restaurants r
		WHERE r.organization_id = $1 AND r.id = $2
		RETURNING id, restaurant_id, special_date::text,
		          COALESCE(opens_at::text, ''), COALESCE(closes_at::text, ''), is_closed, note`,
		organizationID, restaurantID, input.Date, input.OpensAt, input.ClosesAt, input.IsClosed, input.Note,
	))
}

func (s *service) GetSpecialHour(ctx context.Context, organizationID, restaurantID, specialHourID string) (models.SpecialHour, error) {
	return scanSpecialHour(s.db.QueryRowContext(ctx, `
		SELECT sh.id, sh.restaurant_id, sh.special_date::text,
		       COALESCE(sh.opens_at::text, ''), COALESCE(sh.closes_at::text, ''),
		       sh.is_closed, sh.note
		FROM special_hours sh
		JOIN restaurants r ON r.id = sh.restaurant_id
		WHERE r.organization_id = $1 AND sh.restaurant_id = $2 AND sh.id = $3`,
		organizationID, restaurantID, specialHourID,
	))
}

func (s *service) UpdateSpecialHour(
	ctx context.Context,
	organizationID, restaurantID, specialHourID string,
	input models.SpecialHourInput,
	_ models.MutationContext,
) (models.SpecialHour, error) {
	input, err := normalizeSpecialHourInput(input)
	if err != nil {
		return models.SpecialHour{}, err
	}
	return scanSpecialHour(s.db.QueryRowContext(ctx, `
		UPDATE special_hours sh
		SET special_date = $4::date,
		    opens_at = NULLIF($5, '')::time,
		    closes_at = NULLIF($6, '')::time,
		    is_closed = $7,
		    note = $8
		FROM restaurants r
		WHERE sh.id = $3 AND sh.restaurant_id = $2
		  AND r.id = sh.restaurant_id AND r.organization_id = $1
		RETURNING sh.id, sh.restaurant_id, sh.special_date::text,
		          COALESCE(sh.opens_at::text, ''), COALESCE(sh.closes_at::text, ''),
		          sh.is_closed, sh.note`,
		organizationID, restaurantID, specialHourID,
		input.Date, input.OpensAt, input.ClosesAt, input.IsClosed, input.Note,
	))
}

func (s *service) DeleteSpecialHour(
	ctx context.Context,
	organizationID, restaurantID, specialHourID string,
	_ models.MutationContext,
) error {
	result, err := s.db.ExecContext(ctx, `
		DELETE FROM special_hours sh
		USING restaurants r
		WHERE sh.id = $3 AND sh.restaurant_id = $2
		  AND r.id = sh.restaurant_id AND r.organization_id = $1`,
		organizationID, restaurantID, specialHourID,
	)
	return requireAffectedRow(result, err)
}

func normalizeSpecialHourInput(input models.SpecialHourInput) (models.SpecialHourInput, error) {
	input.Date = strings.TrimSpace(input.Date)
	input.Note = strings.TrimSpace(input.Note)
	if _, err := time.Parse(time.DateOnly, input.Date); err != nil {
		return models.SpecialHourInput{}, ErrInvalidInput
	}
	if input.IsClosed {
		input.OpensAt = ""
		input.ClosesAt = ""
		return input, nil
	}
	opensAt, closesAt, err := normalizeClockRange(input.OpensAt, input.ClosesAt)
	if err != nil {
		return models.SpecialHourInput{}, err
	}
	input.OpensAt = opensAt
	input.ClosesAt = closesAt
	return input, nil
}

func normalizeClockRange(opensAt, closesAt string) (string, string, error) {
	opensAt = strings.TrimSpace(opensAt)
	closesAt = strings.TrimSpace(closesAt)
	openTime, openErr := time.Parse("15:04", opensAt)
	closeTime, closeErr := time.Parse("15:04", closesAt)
	if openErr != nil || closeErr != nil || openTime.Equal(closeTime) {
		return "", "", ErrInvalidInput
	}
	return openTime.Format("15:04:05"), closeTime.Format("15:04:05"), nil
}

func scanCatalogItem(row rowScanner) (models.CatalogItem, error) {
	var item models.CatalogItem
	var variantsJSON []byte
	var allergensJSON []byte
	if err := row.Scan(
		&item.ID,
		&item.RestaurantID,
		&item.Name,
		&item.Description,
		&item.PriceCents,
		&variantsJSON,
		&allergensJSON,
		&item.Status,
	); err != nil {
		return models.CatalogItem{}, mapDBError(err)
	}
	if err := json.Unmarshal(variantsJSON, &item.Variants); err != nil {
		return models.CatalogItem{}, fmt.Errorf("decode catalog item variants: %w", err)
	}
	if item.Variants == nil {
		item.Variants = make([]models.CatalogItemVariant, 0)
	}
	if err := json.Unmarshal(allergensJSON, &item.Allergens); err != nil {
		return models.CatalogItem{}, fmt.Errorf("decode catalog item allergens: %w", err)
	}
	if item.Allergens == nil {
		item.Allergens = make([]models.CatalogItemAllergen, 0)
	}
	return item, nil
}

const catalogItemColumns = `
	ci.id, ci.restaurant_id, ci.name, ci.description, ci.price_cents, ci.variants,
	COALESCE((
		SELECT jsonb_agg(
			jsonb_build_object(
				'allergen_id', a.id,
				'name', a.name,
				'code', a.code,
				'description', a.description,
				'relationship', cia.relationship
			)
			ORDER BY a.name, a.id
		)
		FROM catalog_item_allergens cia
		JOIN allergens a
		  ON a.id = cia.allergen_id
		 AND a.organization_id = cia.organization_id
		WHERE cia.catalog_item_id = ci.id
	), '[]'::jsonb),
	ci.status`

func (s *service) ListCatalogItems(ctx context.Context, organizationID, restaurantID string) ([]models.CatalogItem, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+catalogItemColumns+`
		FROM catalog_items ci
		JOIN restaurants r ON r.id = ci.restaurant_id
		WHERE r.organization_id = $1 AND ci.restaurant_id = $2
		ORDER BY ci.name`, organizationID, restaurantID,
	)
	if err != nil {
		return nil, fmt.Errorf("list catalog items: %w", mapDBError(err))
	}
	defer rows.Close()
	items := make([]models.CatalogItem, 0)
	for rows.Next() {
		item, err := scanCatalogItem(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate catalog items: %w", err)
	}
	return items, nil
}

func (s *service) CreateCatalogItem(
	ctx context.Context,
	organizationID, restaurantID string,
	input models.CatalogItemInput,
	_ models.MutationContext,
) (models.CatalogItem, error) {
	input, err := normalizeCatalogItemInput(input)
	if err != nil {
		return models.CatalogItem{}, err
	}
	variantsJSON, err := json.Marshal(input.Variants)
	if err != nil {
		return models.CatalogItem{}, fmt.Errorf("encode catalog item variants: %w", err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return models.CatalogItem{}, fmt.Errorf("begin catalog item creation: %w", err)
	}
	defer tx.Rollback()

	var catalogItemID string
	err = tx.QueryRowContext(ctx, `
		INSERT INTO catalog_items (restaurant_id, name, description, price_cents, variants, status)
		SELECT r.id, $3, $4, $5, $6::jsonb, $7
		FROM restaurants r
		WHERE r.organization_id = $1 AND r.id = $2
		RETURNING id`,
		organizationID, restaurantID, input.Name, input.Description, input.PriceCents, variantsJSON, input.Status,
	).Scan(&catalogItemID)
	if err != nil {
		return models.CatalogItem{}, mapDBError(err)
	}
	if err := replaceCatalogItemAllergens(
		ctx, tx, organizationID, restaurantID, catalogItemID, input.Allergens,
	); err != nil {
		return models.CatalogItem{}, err
	}
	item, err := getCatalogItem(ctx, tx, organizationID, restaurantID, catalogItemID)
	if err != nil {
		return models.CatalogItem{}, err
	}
	if err := tx.Commit(); err != nil {
		return models.CatalogItem{}, fmt.Errorf("commit catalog item creation: %w", err)
	}
	return item, nil
}

func (s *service) GetCatalogItem(ctx context.Context, organizationID, restaurantID, catalogItemID string) (models.CatalogItem, error) {
	return getCatalogItem(ctx, s.db, organizationID, restaurantID, catalogItemID)
}

func getCatalogItem(
	ctx context.Context,
	queryer rowQueryer,
	organizationID, restaurantID, catalogItemID string,
) (models.CatalogItem, error) {
	return scanCatalogItem(queryer.QueryRowContext(ctx, `
		SELECT `+catalogItemColumns+`
		FROM catalog_items ci
		JOIN restaurants r ON r.id = ci.restaurant_id
		WHERE r.organization_id = $1 AND ci.restaurant_id = $2 AND ci.id = $3`,
		organizationID, restaurantID, catalogItemID,
	))
}

func (s *service) UpdateCatalogItem(
	ctx context.Context,
	organizationID, restaurantID, catalogItemID string,
	input models.CatalogItemInput,
	_ models.MutationContext,
) (models.CatalogItem, error) {
	input, err := normalizeCatalogItemInput(input)
	if err != nil {
		return models.CatalogItem{}, err
	}
	variantsJSON, err := json.Marshal(input.Variants)
	if err != nil {
		return models.CatalogItem{}, fmt.Errorf("encode catalog item variants: %w", err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return models.CatalogItem{}, fmt.Errorf("begin catalog item update: %w", err)
	}
	defer tx.Rollback()

	var updatedCatalogItemID string
	err = tx.QueryRowContext(ctx, `
		UPDATE catalog_items ci
		SET name = $4, description = $5, price_cents = $6, variants = $7::jsonb, status = $8
		FROM restaurants r
		WHERE ci.id = $3 AND ci.restaurant_id = $2
		  AND r.id = ci.restaurant_id AND r.organization_id = $1
		RETURNING ci.id`,
		organizationID, restaurantID, catalogItemID,
		input.Name, input.Description, input.PriceCents, variantsJSON, input.Status,
	).Scan(&updatedCatalogItemID)
	if err != nil {
		return models.CatalogItem{}, mapDBError(err)
	}
	if err := replaceCatalogItemAllergens(
		ctx, tx, organizationID, restaurantID, updatedCatalogItemID, input.Allergens,
	); err != nil {
		return models.CatalogItem{}, err
	}
	item, err := getCatalogItem(ctx, tx, organizationID, restaurantID, updatedCatalogItemID)
	if err != nil {
		return models.CatalogItem{}, err
	}
	if err := tx.Commit(); err != nil {
		return models.CatalogItem{}, fmt.Errorf("commit catalog item update: %w", err)
	}
	return item, nil
}

func (s *service) DeleteCatalogItem(
	ctx context.Context,
	organizationID, restaurantID, catalogItemID string,
	_ models.MutationContext,
) error {
	result, err := s.db.ExecContext(ctx, `
		DELETE FROM catalog_items ci
		USING restaurants r
		WHERE ci.id = $3 AND ci.restaurant_id = $2
		  AND r.id = ci.restaurant_id AND r.organization_id = $1`,
		organizationID, restaurantID, catalogItemID,
	)
	return requireAffectedRow(result, err)
}

func normalizeCatalogItemInput(input models.CatalogItemInput) (models.CatalogItemInput, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.Description = strings.TrimSpace(input.Description)
	input.Status = strings.TrimSpace(input.Status)
	if input.Status == "" {
		input.Status = "active"
	}
	if input.Name == "" || input.PriceCents < 0 || len(input.Variants) > 20 ||
		(input.Status != "active" && input.Status != "inactive") {
		return models.CatalogItemInput{}, ErrInvalidInput
	}
	seen := make(map[string]struct{}, len(input.Variants))
	for i := range input.Variants {
		input.Variants[i].Name = strings.TrimSpace(input.Variants[i].Name)
		key := strings.ToLower(input.Variants[i].Name)
		if key == "" || input.Variants[i].PriceCents < 0 {
			return models.CatalogItemInput{}, ErrInvalidInput
		}
		if _, duplicate := seen[key]; duplicate {
			return models.CatalogItemInput{}, ErrInvalidInput
		}
		seen[key] = struct{}{}
	}
	if input.Variants == nil {
		input.Variants = make([]models.CatalogItemVariant, 0)
	}
	if len(input.Allergens) > 64 {
		return models.CatalogItemInput{}, ErrInvalidInput
	}
	seenAllergens := make(map[string]struct{}, len(input.Allergens))
	for i := range input.Allergens {
		input.Allergens[i].AllergenID = strings.TrimSpace(input.Allergens[i].AllergenID)
		input.Allergens[i].Relationship = strings.ToLower(strings.TrimSpace(input.Allergens[i].Relationship))
		allergenID, err := uuid.Parse(input.Allergens[i].AllergenID)
		if err != nil ||
			(input.Allergens[i].Relationship != "contains" && input.Allergens[i].Relationship != "may_contain") {
			return models.CatalogItemInput{}, ErrInvalidInput
		}
		key := allergenID.String()
		if _, duplicate := seenAllergens[key]; duplicate {
			return models.CatalogItemInput{}, ErrInvalidInput
		}
		seenAllergens[key] = struct{}{}
		input.Allergens[i].AllergenID = key
	}
	if input.Allergens == nil {
		input.Allergens = make([]models.CatalogItemAllergenInput, 0)
	}
	return input, nil
}

func replaceCatalogItemAllergens(
	ctx context.Context,
	tx *sql.Tx,
	organizationID, restaurantID, catalogItemID string,
	assignments []models.CatalogItemAllergenInput,
) error {
	if _, err := tx.ExecContext(
		ctx,
		`DELETE FROM catalog_item_allergens WHERE catalog_item_id = $1`,
		catalogItemID,
	); err != nil {
		return mapDBError(err)
	}
	for _, assignment := range assignments {
		result, err := tx.ExecContext(ctx, `
			INSERT INTO catalog_item_allergens (
				catalog_item_id, restaurant_id, organization_id, allergen_id, relationship
			)
			SELECT ci.id, r.id, r.organization_id, a.id, $5
			FROM restaurants r
			JOIN catalog_items ci
			  ON ci.id = $3
			 AND ci.restaurant_id = r.id
			JOIN allergens a
			  ON a.id = $4
			 AND a.organization_id = r.organization_id
			WHERE r.organization_id = $1
			  AND r.id = $2`,
			organizationID,
			restaurantID,
			catalogItemID,
			assignment.AllergenID,
			assignment.Relationship,
		)
		if err != nil {
			return mapDBError(err)
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if rows != 1 {
			return ErrInvalidInput
		}
	}
	return nil
}

func scanIngredient(row rowScanner) (models.Ingredient, error) {
	var ingredient models.Ingredient
	if err := row.Scan(&ingredient.ID, &ingredient.RestaurantID, &ingredient.Name); err != nil {
		return models.Ingredient{}, mapDBError(err)
	}
	return ingredient, nil
}

func (s *service) ListIngredients(ctx context.Context, organizationID, restaurantID string) ([]models.Ingredient, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT i.id, i.restaurant_id, i.name
		FROM ingredients i
		JOIN restaurants r ON r.id = i.restaurant_id
		WHERE r.organization_id = $1 AND i.restaurant_id = $2
		ORDER BY i.name`, organizationID, restaurantID,
	)
	if err != nil {
		return nil, fmt.Errorf("list ingredients: %w", mapDBError(err))
	}
	defer rows.Close()
	ingredients := make([]models.Ingredient, 0)
	for rows.Next() {
		ingredient, err := scanIngredient(rows)
		if err != nil {
			return nil, err
		}
		ingredients = append(ingredients, ingredient)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate ingredients: %w", err)
	}
	return ingredients, nil
}

func (s *service) CreateIngredient(
	ctx context.Context,
	organizationID, restaurantID string,
	input models.IngredientInput,
	_ models.MutationContext,
) (models.Ingredient, error) {
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" {
		return models.Ingredient{}, ErrInvalidInput
	}
	return scanIngredient(s.db.QueryRowContext(ctx, `
		INSERT INTO ingredients (restaurant_id, name)
		SELECT r.id, $3
		FROM restaurants r
		WHERE r.organization_id = $1 AND r.id = $2
		RETURNING id, restaurant_id, name`, organizationID, restaurantID, input.Name,
	))
}

func (s *service) GetIngredient(ctx context.Context, organizationID, restaurantID, ingredientID string) (models.Ingredient, error) {
	return scanIngredient(s.db.QueryRowContext(ctx, `
		SELECT i.id, i.restaurant_id, i.name
		FROM ingredients i
		JOIN restaurants r ON r.id = i.restaurant_id
		WHERE r.organization_id = $1 AND i.restaurant_id = $2 AND i.id = $3`,
		organizationID, restaurantID, ingredientID,
	))
}

func (s *service) UpdateIngredient(
	ctx context.Context,
	organizationID, restaurantID, ingredientID string,
	input models.IngredientInput,
	_ models.MutationContext,
) (models.Ingredient, error) {
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" {
		return models.Ingredient{}, ErrInvalidInput
	}
	return scanIngredient(s.db.QueryRowContext(ctx, `
		UPDATE ingredients i
		SET name = $4
		FROM restaurants r
		WHERE i.id = $3 AND i.restaurant_id = $2
		  AND r.id = i.restaurant_id AND r.organization_id = $1
		RETURNING i.id, i.restaurant_id, i.name`,
		organizationID, restaurantID, ingredientID, input.Name,
	))
}

func (s *service) DeleteIngredient(
	ctx context.Context,
	organizationID, restaurantID, ingredientID string,
	_ models.MutationContext,
) error {
	result, err := s.db.ExecContext(ctx, `
		DELETE FROM ingredients i
		USING restaurants r
		WHERE i.id = $3 AND i.restaurant_id = $2
		  AND r.id = i.restaurant_id AND r.organization_id = $1`,
		organizationID, restaurantID, ingredientID,
	)
	return requireAffectedRow(result, err)
}

func scanMenu(row rowScanner) (models.Menu, error) {
	var menu models.Menu
	if err := row.Scan(&menu.ID, &menu.RestaurantID, &menu.Name, &menu.Description, &menu.Code, &menu.Status); err != nil {
		return models.Menu{}, mapDBError(err)
	}
	return menu, nil
}

func (s *service) ListMenus(ctx context.Context, organizationID, restaurantID string) ([]models.Menu, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT m.id, m.restaurant_id, m.name, m.description, m.code, m.status
		FROM menus m
		JOIN restaurants r ON r.id = m.restaurant_id
		WHERE r.organization_id = $1 AND m.restaurant_id = $2
		ORDER BY m.name`, organizationID, restaurantID,
	)
	if err != nil {
		return nil, fmt.Errorf("list menus: %w", mapDBError(err))
	}
	defer rows.Close()
	menus := make([]models.Menu, 0)
	for rows.Next() {
		menu, err := scanMenu(rows)
		if err != nil {
			return nil, err
		}
		menus = append(menus, menu)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate menus: %w", err)
	}
	return menus, nil
}

func (s *service) CreateMenu(
	ctx context.Context,
	organizationID, restaurantID string,
	input models.MenuInput,
	_ models.MutationContext,
) (models.Menu, error) {
	input, err := normalizeMenuInput(input)
	if err != nil {
		return models.Menu{}, err
	}
	return scanMenu(s.db.QueryRowContext(ctx, `
		INSERT INTO menus (restaurant_id, name, description, code, status)
		SELECT r.id, $3, $4, $5, $6
		FROM restaurants r
		WHERE r.organization_id = $1 AND r.id = $2
		RETURNING id, restaurant_id, name, description, code, status`,
		organizationID, restaurantID, input.Name, input.Description, input.Code, input.Status,
	))
}

func (s *service) GetMenu(ctx context.Context, organizationID, restaurantID, menuID string) (models.Menu, error) {
	return scanMenu(s.db.QueryRowContext(ctx, `
		SELECT m.id, m.restaurant_id, m.name, m.description, m.code, m.status
		FROM menus m
		JOIN restaurants r ON r.id = m.restaurant_id
		WHERE r.organization_id = $1 AND m.restaurant_id = $2 AND m.id = $3`,
		organizationID, restaurantID, menuID,
	))
}

func (s *service) UpdateMenu(
	ctx context.Context,
	organizationID, restaurantID, menuID string,
	input models.MenuInput,
	_ models.MutationContext,
) (models.Menu, error) {
	input, err := normalizeMenuInput(input)
	if err != nil {
		return models.Menu{}, err
	}
	return scanMenu(s.db.QueryRowContext(ctx, `
		UPDATE menus m
		SET name = $4, description = $5, code = $6, status = $7
		FROM restaurants r
		WHERE m.id = $3 AND m.restaurant_id = $2
		  AND r.id = m.restaurant_id AND r.organization_id = $1
		RETURNING m.id, m.restaurant_id, m.name, m.description, m.code, m.status`,
		organizationID, restaurantID, menuID, input.Name, input.Description, input.Code, input.Status,
	))
}

func (s *service) DeleteMenu(
	ctx context.Context,
	organizationID, restaurantID, menuID string,
	_ models.MutationContext,
) error {
	result, err := s.db.ExecContext(ctx, `
		DELETE FROM menus m
		USING restaurants r
		WHERE m.id = $3 AND m.restaurant_id = $2
		  AND r.id = m.restaurant_id AND r.organization_id = $1`,
		organizationID, restaurantID, menuID,
	)
	return requireAffectedRow(result, err)
}

func normalizeMenuInput(input models.MenuInput) (models.MenuInput, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.Description = strings.TrimSpace(input.Description)
	input.Code = strings.TrimSpace(input.Code)
	input.Status = strings.TrimSpace(input.Status)
	if input.Status == "" {
		input.Status = "active"
	}
	validStatus := input.Status == "active" || input.Status == "inactive"
	if input.Name == "" || input.Code == "" || !validStatus {
		return models.MenuInput{}, ErrInvalidInput
	}
	return input, nil
}

func scanMenuSection(row rowScanner) (models.MenuSection, error) {
	var section models.MenuSection
	if err := row.Scan(
		&section.ID,
		&section.RestaurantID,
		&section.MenuID,
		&section.Name,
		&section.Description,
		&section.SortOrder,
		&section.Status,
		&section.CreatedAt,
		&section.UpdatedAt,
	); err != nil {
		return models.MenuSection{}, mapDBError(err)
	}
	section.Entries = make([]models.MenuEntry, 0)
	return section, nil
}

const menuSectionColumns = `
	ms.id, ms.restaurant_id, ms.menu_id, ms.name, ms.description,
	ms.sort_order, ms.status, ms.created_at, ms.updated_at`

func (s *service) ListMenuSections(
	ctx context.Context,
	organizationID, restaurantID, menuID string,
) ([]models.MenuSection, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+menuSectionColumns+`
		FROM menu_sections ms
		JOIN menus m ON m.id = ms.menu_id AND m.restaurant_id = ms.restaurant_id
		JOIN restaurants r ON r.id = ms.restaurant_id
		WHERE r.organization_id = $1 AND ms.restaurant_id = $2 AND ms.menu_id = $3
		ORDER BY ms.sort_order, ms.name, ms.id`, organizationID, restaurantID, menuID,
	)
	if err != nil {
		return nil, fmt.Errorf("list menu sections: %w", mapDBError(err))
	}
	sections := make([]models.MenuSection, 0)
	for rows.Next() {
		section, err := scanMenuSection(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		sections = append(sections, section)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("iterate menu sections: %w", err)
	}
	rows.Close()

	entries, err := s.listMenuEntries(ctx, organizationID, restaurantID, menuID, "")
	if err != nil {
		return nil, err
	}
	sectionIndexes := make(map[string]int, len(sections))
	for i := range sections {
		sectionIndexes[sections[i].ID] = i
	}
	for _, entry := range entries {
		if index, ok := sectionIndexes[entry.MenuSectionID]; ok {
			sections[index].Entries = append(sections[index].Entries, entry)
		}
	}
	return sections, nil
}

func (s *service) CreateMenuSection(
	ctx context.Context,
	organizationID, restaurantID, menuID string,
	input models.MenuSectionInput,
	_ models.MutationContext,
) (models.MenuSection, error) {
	input, err := normalizeMenuSectionInput(input)
	if err != nil {
		return models.MenuSection{}, err
	}
	return scanMenuSection(s.db.QueryRowContext(ctx, `
		INSERT INTO menu_sections (restaurant_id, menu_id, name, description, sort_order, status)
		SELECT m.restaurant_id, m.id, $4, $5, $6, $7
		FROM menus m
		JOIN restaurants r ON r.id = m.restaurant_id
		WHERE r.organization_id = $1 AND m.restaurant_id = $2 AND m.id = $3
		RETURNING id, restaurant_id, menu_id, name, description,
		          sort_order, status, created_at, updated_at`,
		organizationID, restaurantID, menuID,
		input.Name, input.Description, input.SortOrder, input.Status,
	))
}

func (s *service) GetMenuSection(
	ctx context.Context,
	organizationID, restaurantID, menuID, menuSectionID string,
) (models.MenuSection, error) {
	section, err := scanMenuSection(s.db.QueryRowContext(ctx, `
		SELECT `+menuSectionColumns+`
		FROM menu_sections ms
		JOIN menus m ON m.id = ms.menu_id AND m.restaurant_id = ms.restaurant_id
		JOIN restaurants r ON r.id = ms.restaurant_id
		WHERE r.organization_id = $1 AND ms.restaurant_id = $2
		  AND ms.menu_id = $3 AND ms.id = $4`,
		organizationID, restaurantID, menuID, menuSectionID,
	))
	if err != nil {
		return models.MenuSection{}, err
	}
	section.Entries, err = s.listMenuEntries(ctx, organizationID, restaurantID, menuID, menuSectionID)
	if err != nil {
		return models.MenuSection{}, err
	}
	return section, nil
}

func (s *service) UpdateMenuSection(
	ctx context.Context,
	organizationID, restaurantID, menuID, menuSectionID string,
	input models.MenuSectionInput,
	_ models.MutationContext,
) (models.MenuSection, error) {
	input, err := normalizeMenuSectionInput(input)
	if err != nil {
		return models.MenuSection{}, err
	}
	section, err := scanMenuSection(s.db.QueryRowContext(ctx, `
		UPDATE menu_sections ms
		SET name = $5, description = $6, sort_order = $7, status = $8
		FROM menus m, restaurants r
		WHERE ms.id = $4 AND ms.restaurant_id = $2 AND ms.menu_id = $3
		  AND m.id = ms.menu_id AND m.restaurant_id = ms.restaurant_id
		  AND r.id = ms.restaurant_id AND r.organization_id = $1
		RETURNING `+menuSectionColumns,
		organizationID, restaurantID, menuID, menuSectionID,
		input.Name, input.Description, input.SortOrder, input.Status,
	))
	if err != nil {
		return models.MenuSection{}, err
	}
	section.Entries, err = s.listMenuEntries(ctx, organizationID, restaurantID, menuID, menuSectionID)
	if err != nil {
		return models.MenuSection{}, err
	}
	return section, nil
}

func (s *service) DeleteMenuSection(
	ctx context.Context,
	organizationID, restaurantID, menuID, menuSectionID string,
	_ models.MutationContext,
) error {
	result, err := s.db.ExecContext(ctx, `
		DELETE FROM menu_sections ms
		USING menus m, restaurants r
		WHERE ms.id = $4 AND ms.restaurant_id = $2 AND ms.menu_id = $3
		  AND m.id = ms.menu_id AND m.restaurant_id = ms.restaurant_id
		  AND r.id = ms.restaurant_id AND r.organization_id = $1`,
		organizationID, restaurantID, menuID, menuSectionID,
	)
	return requireAffectedRow(result, err)
}

func normalizeMenuSectionInput(input models.MenuSectionInput) (models.MenuSectionInput, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.Description = strings.TrimSpace(input.Description)
	input.Status = strings.TrimSpace(input.Status)
	if input.Status == "" {
		input.Status = "active"
	}
	if input.Name == "" || input.SortOrder < 0 ||
		(input.Status != "active" && input.Status != "inactive") {
		return models.MenuSectionInput{}, ErrInvalidInput
	}
	return input, nil
}

func scanMenuEntry(row rowScanner) (models.MenuEntry, error) {
	var entry models.MenuEntry
	var variantsJSON []byte
	var allergensJSON []byte
	if err := row.Scan(
		&entry.ID,
		&entry.RestaurantID,
		&entry.MenuSectionID,
		&entry.CatalogItemID,
		&entry.SortOrder,
		&entry.Status,
		&entry.CreatedAt,
		&entry.UpdatedAt,
		&entry.CatalogItem.ID,
		&entry.CatalogItem.RestaurantID,
		&entry.CatalogItem.Name,
		&entry.CatalogItem.Description,
		&entry.CatalogItem.PriceCents,
		&variantsJSON,
		&allergensJSON,
		&entry.CatalogItem.Status,
	); err != nil {
		return models.MenuEntry{}, mapDBError(err)
	}
	if err := json.Unmarshal(variantsJSON, &entry.CatalogItem.Variants); err != nil {
		return models.MenuEntry{}, fmt.Errorf("decode menu entry catalog variants: %w", err)
	}
	if entry.CatalogItem.Variants == nil {
		entry.CatalogItem.Variants = make([]models.CatalogItemVariant, 0)
	}
	if err := json.Unmarshal(allergensJSON, &entry.CatalogItem.Allergens); err != nil {
		return models.MenuEntry{}, fmt.Errorf("decode menu entry catalog allergens: %w", err)
	}
	if entry.CatalogItem.Allergens == nil {
		entry.CatalogItem.Allergens = make([]models.CatalogItemAllergen, 0)
	}
	return entry, nil
}

const menuEntryColumns = `
	me.id, me.restaurant_id, me.menu_section_id, me.catalog_item_id,
	me.sort_order, me.status, me.created_at, me.updated_at,
	ci.id, ci.restaurant_id, ci.name, ci.description,
	ci.price_cents, ci.variants,
	COALESCE((
		SELECT jsonb_agg(
			jsonb_build_object(
				'allergen_id', a.id,
				'name', a.name,
				'code', a.code,
				'description', a.description,
				'relationship', cia.relationship
			)
			ORDER BY a.name, a.id
		)
		FROM catalog_item_allergens cia
		JOIN allergens a
		  ON a.id = cia.allergen_id
		 AND a.organization_id = cia.organization_id
		WHERE cia.catalog_item_id = ci.id
	), '[]'::jsonb),
	ci.status`

func (s *service) listMenuEntries(
	ctx context.Context,
	organizationID, restaurantID, menuID, menuSectionID string,
) ([]models.MenuEntry, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+menuEntryColumns+`
		FROM menu_entries me
		JOIN menu_sections ms ON ms.id = me.menu_section_id AND ms.restaurant_id = me.restaurant_id
		JOIN catalog_items ci ON ci.id = me.catalog_item_id AND ci.restaurant_id = me.restaurant_id
		JOIN menus m ON m.id = ms.menu_id AND m.restaurant_id = ms.restaurant_id
		JOIN restaurants r ON r.id = me.restaurant_id
		WHERE r.organization_id = $1 AND me.restaurant_id = $2 AND ms.menu_id = $3
		  AND ($4 = '' OR me.menu_section_id::text = $4)
		ORDER BY ms.sort_order, me.sort_order, ci.name, me.id`,
		organizationID, restaurantID, menuID, menuSectionID,
	)
	if err != nil {
		return nil, fmt.Errorf("list menu entries: %w", mapDBError(err))
	}
	defer rows.Close()
	entries := make([]models.MenuEntry, 0)
	for rows.Next() {
		entry, err := scanMenuEntry(rows)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate menu entries: %w", err)
	}
	return entries, nil
}

func (s *service) ListMenuEntries(
	ctx context.Context,
	organizationID, restaurantID, menuID, menuSectionID string,
) ([]models.MenuEntry, error) {
	return s.listMenuEntries(ctx, organizationID, restaurantID, menuID, menuSectionID)
}

func (s *service) CreateMenuEntry(
	ctx context.Context,
	organizationID, restaurantID, menuID, menuSectionID string,
	input models.MenuEntryInput,
	_ models.MutationContext,
) (models.MenuEntry, error) {
	input, err := normalizeMenuEntryInput(input)
	if err != nil {
		return models.MenuEntry{}, err
	}
	var menuEntryID string
	err = s.db.QueryRowContext(ctx, `
		INSERT INTO menu_entries (
			restaurant_id, menu_section_id, catalog_item_id, sort_order, status
		)
		SELECT ms.restaurant_id, ms.id, ci.id, $6, $7
		FROM menu_sections ms
		JOIN menus m ON m.id = ms.menu_id AND m.restaurant_id = ms.restaurant_id
		JOIN restaurants r ON r.id = ms.restaurant_id
		JOIN catalog_items ci ON ci.id = $5 AND ci.restaurant_id = ms.restaurant_id
		WHERE r.organization_id = $1 AND ms.restaurant_id = $2
		  AND ms.menu_id = $3 AND ms.id = $4
		RETURNING id`,
		organizationID, restaurantID, menuID, menuSectionID,
		input.CatalogItemID, input.SortOrder, input.Status,
	).Scan(&menuEntryID)
	if err != nil {
		return models.MenuEntry{}, mapDBError(err)
	}
	return s.GetMenuEntry(ctx, organizationID, restaurantID, menuID, menuSectionID, menuEntryID)
}

func (s *service) GetMenuEntry(
	ctx context.Context,
	organizationID, restaurantID, menuID, menuSectionID, menuEntryID string,
) (models.MenuEntry, error) {
	return scanMenuEntry(s.db.QueryRowContext(ctx, `
		SELECT `+menuEntryColumns+`
		FROM menu_entries me
		JOIN menu_sections ms ON ms.id = me.menu_section_id AND ms.restaurant_id = me.restaurant_id
		JOIN catalog_items ci ON ci.id = me.catalog_item_id AND ci.restaurant_id = me.restaurant_id
		JOIN menus m ON m.id = ms.menu_id AND m.restaurant_id = ms.restaurant_id
		JOIN restaurants r ON r.id = me.restaurant_id
		WHERE r.organization_id = $1 AND me.restaurant_id = $2
		  AND ms.menu_id = $3 AND me.menu_section_id = $4 AND me.id = $5`,
		organizationID, restaurantID, menuID, menuSectionID, menuEntryID,
	))
}

func (s *service) UpdateMenuEntry(
	ctx context.Context,
	organizationID, restaurantID, menuID, menuSectionID, menuEntryID string,
	input models.MenuEntryInput,
	_ models.MutationContext,
) (models.MenuEntry, error) {
	input, err := normalizeMenuEntryInput(input)
	if err != nil {
		return models.MenuEntry{}, err
	}
	var updatedMenuEntryID string
	err = s.db.QueryRowContext(ctx, `
		UPDATE menu_entries me
		SET catalog_item_id = ci.id, sort_order = $7, status = $8
		FROM menu_sections ms, menus m, restaurants r, catalog_items ci
		WHERE me.id = $5 AND me.restaurant_id = $2 AND me.menu_section_id = $4
		  AND ms.id = me.menu_section_id AND ms.restaurant_id = me.restaurant_id
		  AND ms.menu_id = $3
		  AND m.id = ms.menu_id AND m.restaurant_id = ms.restaurant_id
		  AND r.id = me.restaurant_id AND r.organization_id = $1
		  AND ci.id = $6 AND ci.restaurant_id = me.restaurant_id
		RETURNING me.id`,
		organizationID, restaurantID, menuID, menuSectionID, menuEntryID,
		input.CatalogItemID, input.SortOrder, input.Status,
	).Scan(&updatedMenuEntryID)
	if err != nil {
		return models.MenuEntry{}, mapDBError(err)
	}
	return s.GetMenuEntry(ctx, organizationID, restaurantID, menuID, menuSectionID, updatedMenuEntryID)
}

func (s *service) DeleteMenuEntry(
	ctx context.Context,
	organizationID, restaurantID, menuID, menuSectionID, menuEntryID string,
	_ models.MutationContext,
) error {
	result, err := s.db.ExecContext(ctx, `
		DELETE FROM menu_entries me
		USING menu_sections ms, menus m, restaurants r
		WHERE me.id = $5 AND me.restaurant_id = $2 AND me.menu_section_id = $4
		  AND ms.id = me.menu_section_id AND ms.restaurant_id = me.restaurant_id
		  AND ms.menu_id = $3
		  AND m.id = ms.menu_id AND m.restaurant_id = ms.restaurant_id
		  AND r.id = me.restaurant_id AND r.organization_id = $1`,
		organizationID, restaurantID, menuID, menuSectionID, menuEntryID,
	)
	return requireAffectedRow(result, err)
}

func normalizeMenuEntryInput(input models.MenuEntryInput) (models.MenuEntryInput, error) {
	input.CatalogItemID = strings.TrimSpace(input.CatalogItemID)
	input.Status = strings.TrimSpace(input.Status)
	if input.Status == "" {
		input.Status = "active"
	}
	if _, err := uuid.Parse(input.CatalogItemID); err != nil {
		return models.MenuEntryInput{}, ErrInvalidInput
	}
	if input.SortOrder < 0 || (input.Status != "active" && input.Status != "inactive") {
		return models.MenuEntryInput{}, ErrInvalidInput
	}
	return input, nil
}

func scanMenuSchedule(row rowScanner) (models.MenuSchedule, error) {
	var schedule models.MenuSchedule
	if err := row.Scan(
		&schedule.ID,
		&schedule.RestaurantID,
		&schedule.MenuID,
		&schedule.WeekdayMask,
		&schedule.StartDate,
		&schedule.EndDate,
		&schedule.StartLocalTime,
		&schedule.EndLocalTime,
		&schedule.Priority,
		&schedule.Status,
		&schedule.CreatedAt,
		&schedule.UpdatedAt,
	); err != nil {
		return models.MenuSchedule{}, mapDBError(err)
	}
	return schedule, nil
}

const menuScheduleColumns = `
	ms.id, ms.restaurant_id, ms.menu_id, ms.weekday_mask,
	COALESCE(ms.start_date::text, ''), COALESCE(ms.end_date::text, ''),
	COALESCE(ms.start_local_time::text, ''), COALESCE(ms.end_local_time::text, ''),
	ms.priority, ms.status, ms.created_at, ms.updated_at`

func (s *service) ListMenuSchedules(
	ctx context.Context,
	organizationID, restaurantID, menuID string,
) ([]models.MenuSchedule, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+menuScheduleColumns+`
		FROM menu_schedules ms
		JOIN menus m ON m.id = ms.menu_id AND m.restaurant_id = ms.restaurant_id
		JOIN restaurants r ON r.id = ms.restaurant_id
		WHERE r.organization_id = $1 AND ms.restaurant_id = $2 AND ms.menu_id = $3
		ORDER BY ms.priority DESC, ms.created_at, ms.id`,
		organizationID, restaurantID, menuID,
	)
	if err != nil {
		return nil, fmt.Errorf("list menu schedules: %w", mapDBError(err))
	}
	defer rows.Close()
	schedules := make([]models.MenuSchedule, 0)
	for rows.Next() {
		schedule, err := scanMenuSchedule(rows)
		if err != nil {
			return nil, err
		}
		schedules = append(schedules, schedule)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate menu schedules: %w", err)
	}
	return schedules, nil
}

func (s *service) CreateMenuSchedule(
	ctx context.Context,
	organizationID, restaurantID, menuID string,
	input models.MenuScheduleInput,
	_ models.MutationContext,
) (models.MenuSchedule, error) {
	input, err := normalizeMenuScheduleInput(input)
	if err != nil {
		return models.MenuSchedule{}, err
	}
	return scanMenuSchedule(s.db.QueryRowContext(ctx, `
		INSERT INTO menu_schedules AS ms (
			restaurant_id, menu_id, weekday_mask, start_date, end_date,
			start_local_time, end_local_time, priority, status
		)
		SELECT m.restaurant_id, m.id, $4, NULLIF($5, '')::date, NULLIF($6, '')::date,
		       NULLIF($7, '')::time, NULLIF($8, '')::time, $9, $10
		FROM menus m
		JOIN restaurants r ON r.id = m.restaurant_id
		WHERE r.organization_id = $1 AND m.restaurant_id = $2 AND m.id = $3
		RETURNING `+menuScheduleColumns,
		organizationID, restaurantID, menuID,
		input.WeekdayMask, input.StartDate, input.EndDate,
		input.StartLocalTime, input.EndLocalTime, input.Priority, input.Status,
	))
}

func (s *service) GetMenuSchedule(
	ctx context.Context,
	organizationID, restaurantID, menuScheduleID string,
) (models.MenuSchedule, error) {
	return scanMenuSchedule(s.db.QueryRowContext(ctx, `
		SELECT `+menuScheduleColumns+`
		FROM menu_schedules ms
		JOIN restaurants r ON r.id = ms.restaurant_id
		WHERE r.organization_id = $1 AND ms.restaurant_id = $2 AND ms.id = $3`,
		organizationID, restaurantID, menuScheduleID,
	))
}

func (s *service) UpdateMenuSchedule(
	ctx context.Context,
	organizationID, restaurantID, menuScheduleID string,
	input models.MenuScheduleInput,
	_ models.MutationContext,
) (models.MenuSchedule, error) {
	input, err := normalizeMenuScheduleInput(input)
	if err != nil {
		return models.MenuSchedule{}, err
	}
	return scanMenuSchedule(s.db.QueryRowContext(ctx, `
		UPDATE menu_schedules ms
		SET weekday_mask = $4,
		    start_date = NULLIF($5, '')::date,
		    end_date = NULLIF($6, '')::date,
		    start_local_time = NULLIF($7, '')::time,
		    end_local_time = NULLIF($8, '')::time,
		    priority = $9,
		    status = $10
		FROM restaurants r
		WHERE ms.id = $3 AND ms.restaurant_id = $2
		  AND r.id = ms.restaurant_id AND r.organization_id = $1
		RETURNING `+menuScheduleColumns,
		organizationID, restaurantID, menuScheduleID,
		input.WeekdayMask, input.StartDate, input.EndDate,
		input.StartLocalTime, input.EndLocalTime, input.Priority, input.Status,
	))
}

func (s *service) DeleteMenuSchedule(
	ctx context.Context,
	organizationID, restaurantID, menuScheduleID string,
	_ models.MutationContext,
) error {
	result, err := s.db.ExecContext(ctx, `
		DELETE FROM menu_schedules ms
		USING restaurants r
		WHERE ms.id = $3 AND ms.restaurant_id = $2
		  AND r.id = ms.restaurant_id AND r.organization_id = $1`,
		organizationID, restaurantID, menuScheduleID,
	)
	return requireAffectedRow(result, err)
}

func normalizeMenuScheduleInput(input models.MenuScheduleInput) (models.MenuScheduleInput, error) {
	input.StartDate = strings.TrimSpace(input.StartDate)
	input.EndDate = strings.TrimSpace(input.EndDate)
	input.StartLocalTime = strings.TrimSpace(input.StartLocalTime)
	input.EndLocalTime = strings.TrimSpace(input.EndLocalTime)
	input.Status = strings.TrimSpace(input.Status)
	if input.WeekdayMask == 0 {
		input.WeekdayMask = 127
	}
	if input.Status == "" {
		input.Status = "active"
	}
	if input.WeekdayMask < 1 || input.WeekdayMask > 127 ||
		(input.Status != "active" && input.Status != "inactive") {
		return models.MenuScheduleInput{}, ErrInvalidInput
	}
	var startDate, endDate time.Time
	var err error
	if input.StartDate != "" {
		startDate, err = time.Parse(time.DateOnly, input.StartDate)
		if err != nil {
			return models.MenuScheduleInput{}, ErrInvalidInput
		}
	}
	if input.EndDate != "" {
		endDate, err = time.Parse(time.DateOnly, input.EndDate)
		if err != nil {
			return models.MenuScheduleInput{}, ErrInvalidInput
		}
	}
	if !startDate.IsZero() && !endDate.IsZero() && endDate.Before(startDate) {
		return models.MenuScheduleInput{}, ErrInvalidInput
	}
	if (input.StartLocalTime == "") != (input.EndLocalTime == "") {
		return models.MenuScheduleInput{}, ErrInvalidInput
	}
	if input.StartLocalTime != "" {
		startLocalTime, endLocalTime, err := normalizeClockRange(input.StartLocalTime, input.EndLocalTime)
		if err != nil {
			return models.MenuScheduleInput{}, err
		}
		input.StartLocalTime = startLocalTime
		input.EndLocalTime = endLocalTime
	}
	return input, nil
}

func scanDailySpecial(row rowScanner) (models.DailySpecial, error) {
	var dailySpecial models.DailySpecial
	if err := row.Scan(
		&dailySpecial.ID,
		&dailySpecial.RestaurantID,
		&dailySpecial.Name,
		&dailySpecial.Description,
		&dailySpecial.StartsOn,
		&dailySpecial.EndsOn,
		&dailySpecial.Status,
	); err != nil {
		return models.DailySpecial{}, mapDBError(err)
	}
	return dailySpecial, nil
}

func (s *service) ListDailySpecials(ctx context.Context, organizationID, restaurantID string) ([]models.DailySpecial, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT ds.id, ds.restaurant_id, ds.name, ds.description,
		       ds.starts_on::text, ds.ends_on::text, ds.status
		FROM daily_specials ds
		JOIN restaurants r ON r.id = ds.restaurant_id
		WHERE r.organization_id = $1 AND ds.restaurant_id = $2
		ORDER BY ds.starts_on, ds.name`, organizationID, restaurantID,
	)
	if err != nil {
		return nil, fmt.Errorf("list daily specials: %w", mapDBError(err))
	}
	defer rows.Close()
	dailySpecials := make([]models.DailySpecial, 0)
	for rows.Next() {
		dailySpecial, err := scanDailySpecial(rows)
		if err != nil {
			return nil, err
		}
		dailySpecials = append(dailySpecials, dailySpecial)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate daily specials: %w", err)
	}
	return dailySpecials, nil
}

func (s *service) CreateDailySpecial(
	ctx context.Context,
	organizationID, restaurantID string,
	input models.DailySpecialInput,
	_ models.MutationContext,
) (models.DailySpecial, error) {
	input, err := normalizeDailySpecialInput(input)
	if err != nil {
		return models.DailySpecial{}, err
	}
	return scanDailySpecial(s.db.QueryRowContext(ctx, `
		INSERT INTO daily_specials (restaurant_id, name, description, starts_on, ends_on, status)
		SELECT r.id, $3, $4, $5::date, $6::date, $7
		FROM restaurants r
		WHERE r.organization_id = $1 AND r.id = $2
		RETURNING id, restaurant_id, name, description, starts_on::text, ends_on::text, status`,
		organizationID, restaurantID, input.Name, input.Description, input.StartsOn, input.EndsOn, input.Status,
	))
}

func (s *service) GetDailySpecial(ctx context.Context, organizationID, restaurantID, dailySpecialID string) (models.DailySpecial, error) {
	return scanDailySpecial(s.db.QueryRowContext(ctx, `
		SELECT ds.id, ds.restaurant_id, ds.name, ds.description,
		       ds.starts_on::text, ds.ends_on::text, ds.status
		FROM daily_specials ds
		JOIN restaurants r ON r.id = ds.restaurant_id
		WHERE r.organization_id = $1 AND ds.restaurant_id = $2 AND ds.id = $3`,
		organizationID, restaurantID, dailySpecialID,
	))
}

func (s *service) UpdateDailySpecial(
	ctx context.Context,
	organizationID, restaurantID, dailySpecialID string,
	input models.DailySpecialInput,
	_ models.MutationContext,
) (models.DailySpecial, error) {
	input, err := normalizeDailySpecialInput(input)
	if err != nil {
		return models.DailySpecial{}, err
	}
	return scanDailySpecial(s.db.QueryRowContext(ctx, `
		UPDATE daily_specials ds
		SET name = $4, description = $5, starts_on = $6::date, ends_on = $7::date, status = $8
		FROM restaurants r
		WHERE ds.id = $3 AND ds.restaurant_id = $2
		  AND r.id = ds.restaurant_id AND r.organization_id = $1
		RETURNING ds.id, ds.restaurant_id, ds.name, ds.description,
		          ds.starts_on::text, ds.ends_on::text, ds.status`,
		organizationID, restaurantID, dailySpecialID,
		input.Name, input.Description, input.StartsOn, input.EndsOn, input.Status,
	))
}

func (s *service) DeleteDailySpecial(
	ctx context.Context,
	organizationID, restaurantID, dailySpecialID string,
	_ models.MutationContext,
) error {
	result, err := s.db.ExecContext(ctx, `
		DELETE FROM daily_specials ds
		USING restaurants r
		WHERE ds.id = $3 AND ds.restaurant_id = $2
		  AND r.id = ds.restaurant_id AND r.organization_id = $1`,
		organizationID, restaurantID, dailySpecialID,
	)
	return requireAffectedRow(result, err)
}

func normalizeDailySpecialInput(input models.DailySpecialInput) (models.DailySpecialInput, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.Description = strings.TrimSpace(input.Description)
	input.StartsOn = strings.TrimSpace(input.StartsOn)
	input.EndsOn = strings.TrimSpace(input.EndsOn)
	input.Status = strings.TrimSpace(input.Status)
	if input.Status == "" {
		input.Status = "active"
	}
	startsOn, startErr := time.Parse(time.DateOnly, input.StartsOn)
	endsOn, endErr := time.Parse(time.DateOnly, input.EndsOn)
	validStatus := input.Status == "active" || input.Status == "inactive"
	if input.Name == "" || startErr != nil || endErr != nil || endsOn.Before(startsOn) || !validStatus {
		return models.DailySpecialInput{}, ErrInvalidInput
	}
	return input, nil
}

func scanAllergen(row rowScanner) (models.Allergen, error) {
	var allergen models.Allergen
	if err := row.Scan(
		&allergen.ID,
		&allergen.OrganizationID,
		&allergen.Name,
		&allergen.Code,
		&allergen.Description,
	); err != nil {
		return models.Allergen{}, mapDBError(err)
	}
	return allergen, nil
}

func (s *service) ListAllergens(ctx context.Context, organizationID string) ([]models.Allergen, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, organization_id, name, code, description
		FROM allergens
		WHERE organization_id = $1
		ORDER BY name`, organizationID,
	)
	if err != nil {
		return nil, fmt.Errorf("list allergens: %w", mapDBError(err))
	}
	defer rows.Close()
	allergens := make([]models.Allergen, 0)
	for rows.Next() {
		allergen, err := scanAllergen(rows)
		if err != nil {
			return nil, err
		}
		allergens = append(allergens, allergen)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate allergens: %w", err)
	}
	return allergens, nil
}

func (s *service) CreateAllergen(
	ctx context.Context,
	organizationID string,
	input models.AllergenInput,
	_ models.MutationContext,
) (models.Allergen, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.Code = strings.TrimSpace(input.Code)
	input.Description = strings.TrimSpace(input.Description)
	if organizationID == "" || input.Name == "" || input.Code == "" {
		return models.Allergen{}, ErrInvalidInput
	}
	return scanAllergen(s.db.QueryRowContext(ctx, `
		INSERT INTO allergens (organization_id, name, code, description)
		VALUES ($1, $2, $3, $4)
		RETURNING id, organization_id, name, code, description`,
		organizationID, input.Name, input.Code, input.Description,
	))
}

func (s *service) GetAllergen(ctx context.Context, organizationID, allergenID string) (models.Allergen, error) {
	return scanAllergen(s.db.QueryRowContext(ctx, `
		SELECT id, organization_id, name, code, description
		FROM allergens
		WHERE organization_id = $1 AND id = $2`, organizationID, allergenID,
	))
}

func (s *service) UpdateAllergen(
	ctx context.Context,
	organizationID, allergenID string,
	input models.AllergenInput,
	_ models.MutationContext,
) (models.Allergen, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.Code = strings.TrimSpace(input.Code)
	input.Description = strings.TrimSpace(input.Description)
	if input.Name == "" || input.Code == "" {
		return models.Allergen{}, ErrInvalidInput
	}
	return scanAllergen(s.db.QueryRowContext(ctx, `
		UPDATE allergens
		SET name = $3, code = $4, description = $5
		WHERE organization_id = $1 AND id = $2
		RETURNING id, organization_id, name, code, description`,
		organizationID, allergenID, input.Name, input.Code, input.Description,
	))
}

func (s *service) DeleteAllergen(ctx context.Context, organizationID, allergenID string, _ models.MutationContext) error {
	result, err := s.db.ExecContext(ctx, `
		DELETE FROM allergens WHERE organization_id = $1 AND id = $2`, organizationID, allergenID,
	)
	return requireAffectedRow(result, err)
}
