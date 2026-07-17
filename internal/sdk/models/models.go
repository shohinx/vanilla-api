package models

import "time"

type Menu struct {
	GeneratedAt time.Time  `json:"generated_at"`
	Categories  []Category `json:"categories"`
}

type Category struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Items       []Item `json:"items"`
}

type MenuCategory struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	SortOrder   int    `json:"sort_order"`
}

type NewCategory struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	SortOrder   int    `json:"sort_order,omitempty"`
}

type Item struct {
	ID                string          `json:"id"`
	Name              string          `json:"name"`
	Description       string          `json:"description,omitempty"`
	Type              string          `json:"type"`
	ImageURL          string          `json:"image_url,omitempty"`
	PriceCents        int             `json:"price_cents"`
	Currency          string          `json:"currency"`
	Available         bool            `json:"available"`
	QuantityAvailable int             `json:"-"`
	ModifierGroups    []ModifierGroup `json:"modifier_groups,omitempty"`
}

type ItemImage struct {
	ItemID    string    `json:"item_id"`
	ImageURL  string    `json:"image_url"`
	UpdatedAt time.Time `json:"updated_at"`
}

type ModifierGroup struct {
	ID            string           `json:"id"`
	Name          string           `json:"name"`
	MinSelections int              `json:"min_selections"`
	MaxSelections int              `json:"max_selections"`
	Options       []ModifierOption `json:"options"`
}

type ModifierOption struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	PriceDeltaCents int    `json:"price_delta_cents"`
	Available       bool   `json:"available"`
}

type NewItem struct {
	ID             string             `json:"id"`
	CategoryID     string             `json:"category_id"`
	Name           string             `json:"name"`
	Description    string             `json:"description,omitempty"`
	Type           string             `json:"type"`
	ImageURL       *string            `json:"image_url,omitempty"`
	PriceCents     int                `json:"price_cents"`
	Currency       string             `json:"currency"`
	Quantity       int                `json:"quantity"`
	SortOrder      int                `json:"sort_order,omitempty"`
	ModifierGroups []NewModifierGroup `json:"modifier_groups,omitempty"`
}

type NewModifierGroup struct {
	ID            string              `json:"id"`
	Name          string              `json:"name"`
	MinSelections int                 `json:"min_selections"`
	MaxSelections int                 `json:"max_selections"`
	SortOrder     int                 `json:"sort_order,omitempty"`
	Options       []NewModifierOption `json:"options"`
}

type NewModifierOption struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	PriceDeltaCents int    `json:"price_delta_cents"`
	Available       *bool  `json:"available,omitempty"`
	SortOrder       int    `json:"sort_order,omitempty"`
}

type Inventory struct {
	ItemID    string    `json:"item_id"`
	Quantity  int       `json:"quantity"`
	Available bool      `json:"available"`
	UpdatedAt time.Time `json:"updated_at"`
}

type QuoteRequest struct {
	Items []QuoteItemRequest `json:"items"`
}

type SubmitOrderRequest struct {
	CustomerName string             `json:"customer_name"`
	Notes        string             `json:"notes,omitempty"`
	Items        []QuoteItemRequest `json:"items"`
}

type QuoteItemRequest struct {
	ItemID    string   `json:"item_id"`
	Quantity  int      `json:"quantity"`
	OptionIDs []string `json:"option_ids,omitempty"`
}

type Quote struct {
	Currency      string          `json:"currency"`
	SubtotalCents int             `json:"subtotal_cents"`
	Items         []QuoteLineItem `json:"items"`
}

type QuoteLineItem struct {
	ItemID         string                `json:"item_id"`
	Name           string                `json:"name"`
	Quantity       int                   `json:"quantity"`
	UnitPriceCents int                   `json:"unit_price_cents"`
	LineTotalCents int                   `json:"line_total_cents"`
	Options        []SelectedQuoteOption `json:"options,omitempty"`
}

type SelectedQuoteOption struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	PriceDeltaCents int    `json:"price_delta_cents"`
}

type Order struct {
	ID            string          `json:"id"`
	OrderNumber   string          `json:"order_number"`
	CustomerName  string          `json:"customer_name"`
	Notes         string          `json:"notes,omitempty"`
	Status        string          `json:"status"`
	Currency      string          `json:"currency"`
	SubtotalCents int             `json:"subtotal_cents"`
	Items         []QuoteLineItem `json:"items"`
	CreatedAt     time.Time       `json:"created_at"`
	SoldAt        *time.Time      `json:"sold_at,omitempty"`
}

type Link struct {
	ID          string    `json:"id"`
	ShortLink   string    `json:"short_link"`
	QRCode      string    `json:"qr_code"`
	Destination string    `json:"destination"`
	CreatedAt   time.Time `json:"created_at"`
}

type ValidationError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

type ValidationErrors []ValidationError

func (e ValidationErrors) Error() string {
	return "request validation failed"
}
