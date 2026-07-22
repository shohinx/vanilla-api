package models

import (
	"time"
)

const (
	OrderStatusNew       = "new"
	OrderStatusSold      = "sold"
	OrderStatusCancelled = "cancelled"
)

type Role string

const (
	RoleOwner Role = "owner"
	RoleAdmin Role = "admin"
	RoleUser  Role = "user"
)

func (r Role) Valid() bool {
	return r == RoleUser || r == RoleAdmin || r == RoleOwner
}

type Principal struct {
	ID             string `json:"id"`
	OrganizationID string `json:"organization_id"`
	Role           Role   `json:"role"`
}

func (p Principal) CanAdminister() bool {
	return p.Role == RoleAdmin || p.Role == RoleOwner
}

// RefreshToken represents a refresh token for a user
type RefreshToken struct {
	ID        string     `json:"id"`
	UserID    string     `json:"user_id"`
	Token     []byte     `json:"-"`
	ExpiresAt time.Time  `json:"expires_at"`
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

type NewRefreshToken struct {
	UserID    string
	Token     []byte
	ExpiresAt time.Time
}

// User represents a user in the system
type User struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Username      string    `json:"username"`
	Email         string    `json:"email"`
	Password      []byte    `json:"-"`
	AvatarPhotoID *int      `json:"avatar_photo_id,omitempty"`
	Role          Role      `json:"role"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

type NewUser struct {
	Name            string `json:"name"`
	Username        string `json:"username"`
	Email           string `json:"email"`
	Password        []byte `json:"password"`
	PasswordConfirm []byte `json:"password_confirm"`
}

type UpdateUsername struct {
	Username string `json:"username"`
}

// PasswordResetToken represents a password reset token for a user
type PasswordResetToken struct {
	ID        string     `json:"id"`
	UserID    string     `json:"user_id"`
	Token     string     `json:"-"`
	ExpiresAt time.Time  `json:"expires_at"`
	UsedAt    *time.Time `json:"used_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
}

type NewPasswordResetToken struct {
	UserID    string
	Token     string
	ExpiresAt time.Time
}

type PublicMenuSnapshot struct {
	Payload     []byte
	ETag        string
	GeneratedAt time.Time
}

// Admin

type tenantScope uint8

const (
	scopeGlobal tenantScope = iota
	scopeOrganization
	scopeRestaurant
)

type AdminScope struct {
	OrganizationID string
	RestaurantID   string
}

type MutationContext struct {
	ActorID   string
	RequestID string
}

type Membership struct {
	UserID         string `json:"user_id"`
	OrganizationID string `json:"organization_id"`
	Role           string `json:"role"`
	Status         string `json:"status"`
}

type MembershipInput struct {
	AuthSubject string
	Email       string
	Role        string
	Status      string
}

type Organization struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Slug      string    `json:"slug"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type OrganizationInput struct {
	Name string `json:"name"`
	Slug string `json:"slug"`
}

type Restaurant struct {
	ID             string `json:"id"`
	OrganizationID string `json:"organization_id"`
	Name           string `json:"name"`
	Slug           string `json:"slug"`
	Timezone       string `json:"timezone"`
	Currency       string `json:"currency"`
	Status         string `json:"status"`
}

type RestaurantInput struct {
	Name     string `json:"name"`
	Slug     string `json:"slug"`
	Timezone string `json:"timezone"`
	Currency string `json:"currency"`
	Status   string `json:"status"`
}

type BusinessHour struct {
	ID           string `json:"id"`
	RestaurantID string `json:"restaurant_id"`
	DayOfWeek    int    `json:"day_of_week"`
	OpensAt      string `json:"opens_at"`
	ClosesAt     string `json:"closes_at"`
}

type BusinessHourInput struct {
	DayOfWeek int    `json:"day_of_week"`
	OpensAt   string `json:"opens_at"`
	ClosesAt  string `json:"closes_at"`
}

type SpecialHour struct {
	ID           string `json:"id"`
	RestaurantID string `json:"restaurant_id"`
	Date         string `json:"date"`
	OpensAt      string `json:"opens_at,omitempty"`
	ClosesAt     string `json:"closes_at,omitempty"`
	IsClosed     bool   `json:"is_closed"`
	Note         string `json:"note,omitempty"`
}

type SpecialHourInput struct {
	Date     string `json:"date"`
	OpensAt  string `json:"opens_at,omitempty"`
	ClosesAt string `json:"closes_at,omitempty"`
	IsClosed bool   `json:"is_closed"`
	Note     string `json:"note,omitempty"`
}

type CatalogItem struct {
	ID           string               `json:"id"`
	RestaurantID string               `json:"restaurant_id"`
	Name         string               `json:"name"`
	Description  string               `json:"description,omitempty"`
	PriceCents   int                  `json:"price_cents"`
	Variants     []CatalogItemVariant `json:"variants"`
	Status       string               `json:"status"`
}

type CatalogItemInput struct {
	Name        string               `json:"name"`
	Description string               `json:"description,omitempty"`
	PriceCents  int                  `json:"price_cents"`
	Variants    []CatalogItemVariant `json:"variants"`
	Status      string               `json:"status"`
}

type CatalogItemVariant struct {
	Name       string `json:"name"`
	PriceCents int    `json:"price_cents"`
}

type Ingredient struct {
	ID           string `json:"id"`
	RestaurantID string `json:"restaurant_id"`
	Name         string `json:"name"`
}

type IngredientInput struct {
	Name string `json:"name"`
}

type Menu struct {
	ID           string `json:"id"`
	RestaurantID string `json:"restaurant_id"`
	Name         string `json:"name"`
	Description  string `json:"description,omitempty"`
	Code         string `json:"code"`
	Status       string `json:"status"`
}

type MenuInput struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Code        string `json:"code"`
	Status      string `json:"status"`
}

type MenuSchedule struct {
	ID             string    `json:"id"`
	RestaurantID   string    `json:"restaurant_id"`
	MenuID         string    `json:"menu_id"`
	WeekdayMask    int       `json:"weekday_mask"`
	StartDate      string    `json:"start_date,omitempty"`
	EndDate        string    `json:"end_date,omitempty"`
	StartLocalTime string    `json:"start_local_time,omitempty"`
	EndLocalTime   string    `json:"end_local_time,omitempty"`
	Priority       int       `json:"priority"`
	Status         string    `json:"status"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type MenuScheduleInput struct {
	WeekdayMask    int    `json:"weekday_mask"`
	StartDate      string `json:"start_date,omitempty"`
	EndDate        string `json:"end_date,omitempty"`
	StartLocalTime string `json:"start_local_time,omitempty"`
	EndLocalTime   string `json:"end_local_time,omitempty"`
	Priority       int    `json:"priority"`
	Status         string `json:"status"`
}

type MenuSection struct {
	ID           string      `json:"id"`
	RestaurantID string      `json:"restaurant_id"`
	MenuID       string      `json:"menu_id"`
	Name         string      `json:"name"`
	Description  string      `json:"description,omitempty"`
	SortOrder    int         `json:"sort_order"`
	Status       string      `json:"status"`
	Entries      []MenuEntry `json:"entries"`
	CreatedAt    time.Time   `json:"created_at"`
	UpdatedAt    time.Time   `json:"updated_at"`
}

type MenuSectionInput struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	SortOrder   int    `json:"sort_order"`
	Status      string `json:"status"`
}

type MenuEntry struct {
	ID            string      `json:"id"`
	RestaurantID  string      `json:"restaurant_id"`
	MenuSectionID string      `json:"menu_section_id"`
	CatalogItemID string      `json:"catalog_item_id"`
	SortOrder     int         `json:"sort_order"`
	Status        string      `json:"status"`
	CatalogItem   CatalogItem `json:"catalog_item"`
	CreatedAt     time.Time   `json:"created_at"`
	UpdatedAt     time.Time   `json:"updated_at"`
}

type MenuEntryInput struct {
	CatalogItemID string `json:"catalog_item_id"`
	SortOrder     int    `json:"sort_order"`
	Status        string `json:"status"`
}

type DailySpecial struct {
	ID           string `json:"id"`
	RestaurantID string `json:"restaurant_id"`
	Name         string `json:"name"`
	Description  string `json:"description,omitempty"`
	StartsOn     string `json:"starts_on"`
	EndsOn       string `json:"ends_on"`
	Status       string `json:"status"`
}

type DailySpecialInput struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	StartsOn    string `json:"starts_on"`
	EndsOn      string `json:"ends_on"`
	Status      string `json:"status"`
}

type Allergen struct {
	ID             string `json:"id"`
	OrganizationID string `json:"organization_id"`
	Name           string `json:"name"`
	Code           string `json:"code"`
	Description    string `json:"description"`
}

type AllergenInput struct {
	Name        string `json:"name"`
	Code        string `json:"code"`
	Description string `json:"description"`
}
