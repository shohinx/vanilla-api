package models

import (
	"iter"
	"time"
)

const (
	OrderStatusNew       = "new"
	OrderStatusSold      = "sold"
	OrderStatusCancelled = "cancelled"
)

type Menu struct {
	GeneratedAt time.Time  `json:"generated_at"`
	Categories  []Category `json:"categories"`
}

// Items returns every menu item in category order without allocating an
// intermediate slice.
func (m Menu) Items() iter.Seq[Item] {
	return func(yield func(Item) bool) {
		for _, category := range m.Categories {
			for _, item := range category.Items {
				if !yield(item) {
					return
				}
			}
		}
	}
}

type Category struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	SortOrder int    `json:"sort_order"`
	Items     []Item `json:"items"`
}

type MenuCategory struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	SortOrder int    `json:"sort_order"`
}

type NewCategory struct {
	Name      string `json:"name"`
	SortOrder int    `json:"sort_order,omitempty"`
}

type Item struct {
	ID            int64          `json:"id"`
	CategoryID    int64          `json:"category_id,omitempty"`
	Name          string         `json:"name"`
	Description   string         `json:"description,omitempty"`
	ImageURL      string         `json:"image_url,omitempty"`
	PriceCents    int            `json:"price_cents"`
	Available     bool           `json:"available"`
	TrackStock    bool           `json:"track_stock"`
	StockQuantity *int           `json:"stock_qty,omitempty"`
	VariantGroups []VariantGroup `json:"variant_groups,omitempty"`
}

type ItemImage struct {
	ItemID    int64     `json:"item_id"`
	ImageURL  string    `json:"image_url"`
	UpdatedAt time.Time `json:"updated_at"`
}

type VariantGroup struct {
	ID       int64           `json:"id"`
	Name     string          `json:"name"`
	Required bool            `json:"required"`
	Options  []VariantOption `json:"options"`
}

type VariantOption struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	PriceCents    int    `json:"price_cents"`
	Available     bool   `json:"available"`
	TrackStock    bool   `json:"track_stock"`
	StockQuantity *int   `json:"stock_qty,omitempty"`
}

type NewItem struct {
	CategoryID    int64            `json:"category_id"`
	Name          string           `json:"name"`
	Description   string           `json:"description,omitempty"`
	ImageURL      *string          `json:"image_url,omitempty"`
	PriceCents    int              `json:"price_cents"`
	TrackStock    bool             `json:"track_stock"`
	StockQuantity *int             `json:"stock_qty,omitempty"`
	Available     *bool            `json:"available,omitempty"`
	SortOrder     int              `json:"sort_order,omitempty"`
	VariantGroup  *NewVariantGroup `json:"variant_group,omitempty"`
}

type NewVariantGroup struct {
	Name     string             `json:"name"`
	Required *bool              `json:"required,omitempty"`
	Options  []NewVariantOption `json:"options"`
}

type NewVariantOption struct {
	Name          string `json:"name"`
	PriceCents    int    `json:"price_cents"`
	TrackStock    bool   `json:"track_stock"`
	StockQuantity *int   `json:"stock_qty,omitempty"`
}

type Inventory struct {
	ItemID    int64     `json:"item_id"`
	Quantity  int       `json:"stock_qty"`
	Available bool      `json:"available"`
	UpdatedAt time.Time `json:"updated_at"`
}

type VariantInventory struct {
	VariantOptionID int64 `json:"variant_option_id"`
	Quantity        int   `json:"stock_qty"`
	Available       bool  `json:"available"`
}

type ItemAvailability struct {
	ItemID    int64     `json:"item_id"`
	Available bool      `json:"available"`
	UpdatedAt time.Time `json:"updated_at"`
}

type QuoteRequest struct {
	Items []QuoteItemRequest `json:"items"`
}

type SubmitOrderRequest struct {
	TableNumber string             `json:"table_number"`
	GuestCount  int                `json:"guest_count"`
	Items       []QuoteItemRequest `json:"items"`
}

type QuoteItemRequest struct {
	ItemID          int64  `json:"item_id"`
	VariantOptionID *int64 `json:"variant_option_id,omitempty"`
	Quantity        int    `json:"quantity"`
}

type Quote struct {
	TotalCents int             `json:"total_cents"`
	Items      []QuoteLineItem `json:"items"`
}

type QuoteLineItem struct {
	ItemID         int64                  `json:"item_id"`
	Name           string                 `json:"name"`
	Variant        *SelectedVariantOption `json:"variant,omitempty"`
	Quantity       int                    `json:"quantity"`
	UnitPriceCents int                    `json:"unit_price_cents"`
	LineTotalCents int                    `json:"line_total_cents"`
}

type SelectedVariantOption struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type Order struct {
	ID          int64           `json:"id"`
	OrderNumber string          `json:"order_number"`
	StaffID     *int64          `json:"staff_id,omitempty"`
	StaffName   string          `json:"staff_name,omitempty"`
	TableNumber string          `json:"table_number"`
	GuestCount  int             `json:"guest_count"`
	Status      string          `json:"status"`
	TotalCents  int             `json:"total_cents"`
	Items       []QuoteLineItem `json:"items"`
	CreatedAt   time.Time       `json:"created_at"`
	SoldAt      *time.Time      `json:"sold_at,omitempty"`
}

type Staff struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Active    bool      `json:"active"`
	CreatedAt time.Time `json:"created_at"`
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
