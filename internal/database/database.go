package database

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "github.com/joho/godotenv/autoload"

	"github.com/shohinx/vanilla-api/internal/dub"
	"github.com/shohinx/vanilla-api/internal/menu"
)

type Service interface {
	Initialize(context.Context) error
	Health(context.Context) map[string]string
	Menu(context.Context, bool) (menu.Menu, error)
	Categories(context.Context) ([]menu.MenuCategory, error)
	CreateCategories(context.Context, []menu.NewCategory) ([]menu.MenuCategory, error)
	UpdateInventory(context.Context, string, int) (menu.Inventory, error)
	UpdateItemImage(context.Context, string, string) (menu.ItemImage, error)
	CreateMenuItems(context.Context, []menu.NewItem) ([]menu.Item, error)
	CreateOrder(context.Context, menu.SubmitOrderRequest, menu.Quote) (menu.Order, error)
	Orders(context.Context, string) ([]menu.Order, error)
	UpdateOrderStatus(context.Context, string, string) (menu.Order, error)
	MenuQR(context.Context) (dub.Link, error)
	SaveMenuQR(context.Context, dub.Link) error
	Close() error
}

type service struct {
	db *sql.DB
}

var (
	database = os.Getenv("BLUEPRINT_DB_DATABASE")
	password = os.Getenv("BLUEPRINT_DB_PASSWORD")
	username = os.Getenv("BLUEPRINT_DB_USERNAME")
	port     = os.Getenv("BLUEPRINT_DB_PORT")
	host     = os.Getenv("BLUEPRINT_DB_HOST")
	schema   = os.Getenv("BLUEPRINT_DB_SCHEMA")
)

var (
	ErrNotFound          = errors.New("not found")
	ErrInsufficientStock = errors.New("insufficient stock")
	ErrInvalidTransition = errors.New("invalid order status transition")
	ErrConflict          = errors.New("resource already exists")
	ErrInvalidCategory   = errors.New("category does not exist")
)

func New() Service {
	if database == "" {
		database = "vanilla_api"
	}
	if username == "" {
		username = "postgres"
	}
	if password == "" {
		password = "postgres"
	}
	if port == "" {
		port = "5432"
	}
	if host == "" {
		host = "localhost"
	}
	if schema == "" {
		schema = "public"
	}

	connection := &url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(username, password),
		Host:   host + ":" + port,
		Path:   database,
	}
	query := connection.Query()
	query.Set("sslmode", envOrDefault("BLUEPRINT_DB_SSLMODE", "disable"))
	query.Set("search_path", schema)
	connection.RawQuery = query.Encode()

	db, _ := sql.Open("pgx", connection.String())
	db.SetMaxOpenConns(20)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(30 * time.Minute)
	return &service{db: db}
}

func (s *service) Initialize(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := s.db.PingContext(ctx); err != nil {
		return fmt.Errorf("connect to postgres: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, schemaSQL); err != nil {
		return fmt.Errorf("initialize database schema: %w", err)
	}
	return nil
}

func (s *service) Health(parent context.Context) map[string]string {
	ctx, cancel := context.WithTimeout(parent, time.Second)
	defer cancel()

	stats := map[string]string{"status": "up", "message": "database is healthy"}
	if err := s.db.PingContext(ctx); err != nil {
		return map[string]string{"status": "down", "error": err.Error()}
	}

	dbStats := s.db.Stats()
	stats["open_connections"] = strconv.Itoa(dbStats.OpenConnections)
	stats["in_use"] = strconv.Itoa(dbStats.InUse)
	stats["idle"] = strconv.Itoa(dbStats.Idle)
	return stats
}

func (s *service) Menu(ctx context.Context, includeUnavailable bool) (menu.Menu, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT c.id, c.name, c.description,
		       i.id, i.name, i.description, i.item_type, i.image_url,
		       i.price_cents, i.currency, i.quantity_available
		FROM categories c
		JOIN menu_items i ON i.category_id = c.id
		WHERE i.is_active = TRUE AND ($1 OR i.quantity_available > 0)
		ORDER BY c.sort_order, c.name, i.sort_order, i.name`, includeUnavailable)
	if err != nil {
		return menu.Menu{}, fmt.Errorf("query menu items: %w", err)
	}
	defer rows.Close()

	type categoryData struct {
		category menu.Category
		itemIDs  []string
	}
	categories := make([]categoryData, 0)
	categoryIndexes := make(map[string]int)
	items := make(map[string]*menu.Item)

	for rows.Next() {
		var categoryID, categoryName, categoryDescription string
		item := &menu.Item{}
		if err := rows.Scan(
			&categoryID, &categoryName, &categoryDescription,
			&item.ID, &item.Name, &item.Description, &item.Type, &item.ImageURL,
			&item.PriceCents, &item.Currency, &item.QuantityAvailable,
		); err != nil {
			return menu.Menu{}, fmt.Errorf("scan menu item: %w", err)
		}
		item.Available = item.QuantityAvailable > 0
		item.ModifierGroups = []menu.ModifierGroup{}
		items[item.ID] = item

		categoryIndex, found := categoryIndexes[categoryID]
		if !found {
			categoryIndex = len(categories)
			categoryIndexes[categoryID] = categoryIndex
			categories = append(categories, categoryData{category: menu.Category{
				ID: categoryID, Name: categoryName, Description: categoryDescription, Items: []menu.Item{},
			}})
		}
		categories[categoryIndex].itemIDs = append(categories[categoryIndex].itemIDs, item.ID)
	}
	if err := rows.Err(); err != nil {
		return menu.Menu{}, fmt.Errorf("iterate menu items: %w", err)
	}

	modifierRows, err := s.db.QueryContext(ctx, `
		SELECT mg.id, mg.item_id, mg.name, mg.min_selections, mg.max_selections,
		       mo.id, mo.name, mo.price_delta_cents, mo.is_available
		FROM modifier_groups mg
		JOIN modifier_options mo ON mo.group_id = mg.id
		JOIN menu_items i ON i.id = mg.item_id
		WHERE i.is_active = TRUE
		ORDER BY mg.sort_order, mg.name, mo.sort_order, mo.name`)
	if err != nil {
		return menu.Menu{}, fmt.Errorf("query modifiers: %w", err)
	}
	defer modifierRows.Close()

	groupIndexes := make(map[string]int)
	for modifierRows.Next() {
		var groupID, itemID, groupName string
		var minSelections, maxSelections int
		var option menu.ModifierOption
		if err := modifierRows.Scan(
			&groupID, &itemID, &groupName, &minSelections, &maxSelections,
			&option.ID, &option.Name, &option.PriceDeltaCents, &option.Available,
		); err != nil {
			return menu.Menu{}, fmt.Errorf("scan modifier: %w", err)
		}
		item, found := items[itemID]
		if !found {
			continue
		}
		groupIndex, found := groupIndexes[groupID]
		if !found {
			groupIndex = len(item.ModifierGroups)
			groupIndexes[groupID] = groupIndex
			item.ModifierGroups = append(item.ModifierGroups, menu.ModifierGroup{
				ID: groupID, Name: groupName, MinSelections: minSelections,
				MaxSelections: maxSelections, Options: []menu.ModifierOption{},
			})
		}
		item.ModifierGroups[groupIndex].Options = append(item.ModifierGroups[groupIndex].Options, option)
	}
	if err := modifierRows.Err(); err != nil {
		return menu.Menu{}, fmt.Errorf("iterate modifiers: %w", err)
	}

	result := menu.Menu{GeneratedAt: time.Now().UTC(), Categories: make([]menu.Category, 0, len(categories))}
	for _, data := range categories {
		for _, itemID := range data.itemIDs {
			data.category.Items = append(data.category.Items, *items[itemID])
		}
		result.Categories = append(result.Categories, data.category)
	}
	return result, nil
}

func (s *service) Categories(ctx context.Context) ([]menu.MenuCategory, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, description, sort_order
		FROM categories
		ORDER BY sort_order, name`)
	if err != nil {
		return nil, fmt.Errorf("query categories: %w", err)
	}
	defer rows.Close()
	categories := make([]menu.MenuCategory, 0)
	for rows.Next() {
		var category menu.MenuCategory
		if err := rows.Scan(&category.ID, &category.Name, &category.Description, &category.SortOrder); err != nil {
			return nil, fmt.Errorf("scan category: %w", err)
		}
		categories = append(categories, category)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate categories: %w", err)
	}
	return categories, nil
}

func (s *service) CreateCategories(ctx context.Context, newCategories []menu.NewCategory) ([]menu.MenuCategory, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin category transaction: %w", err)
	}
	defer tx.Rollback()
	created := make([]menu.MenuCategory, 0, len(newCategories))
	for _, newCategory := range newCategories {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO categories (id, name, description, sort_order)
			VALUES ($1, $2, $3, $4)`,
			newCategory.ID, newCategory.Name, newCategory.Description, newCategory.SortOrder,
		)
		if err != nil {
			return nil, menuItemWriteError(err)
		}
		created = append(created, menu.MenuCategory{
			ID: newCategory.ID, Name: newCategory.Name,
			Description: newCategory.Description, SortOrder: newCategory.SortOrder,
		})
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit categories: %w", err)
	}
	return created, nil
}

func (s *service) UpdateInventory(ctx context.Context, itemID string, quantity int) (menu.Inventory, error) {
	var inventory menu.Inventory
	err := s.db.QueryRowContext(ctx, `
		UPDATE menu_items
		SET quantity_available = $2, updated_at = NOW()
		WHERE id = $1 AND is_active = TRUE
		RETURNING id, quantity_available, updated_at`, itemID, quantity).Scan(
		&inventory.ItemID, &inventory.Quantity, &inventory.UpdatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return menu.Inventory{}, ErrNotFound
		}
		return menu.Inventory{}, fmt.Errorf("update inventory: %w", err)
	}
	inventory.Available = inventory.Quantity > 0
	return inventory, nil
}

func (s *service) UpdateItemImage(ctx context.Context, itemID, imageURL string) (menu.ItemImage, error) {
	var image menu.ItemImage
	err := s.db.QueryRowContext(ctx, `
		UPDATE menu_items
		SET image_url = $2, updated_at = NOW()
		WHERE id = $1 AND is_active = TRUE
		RETURNING id, image_url, updated_at`, itemID, imageURL).Scan(
		&image.ItemID, &image.ImageURL, &image.UpdatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return menu.ItemImage{}, ErrNotFound
		}
		return menu.ItemImage{}, fmt.Errorf("update item image: %w", err)
	}
	return image, nil
}

func (s *service) CreateMenuItems(ctx context.Context, newItems []menu.NewItem) ([]menu.Item, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin menu item transaction: %w", err)
	}
	defer tx.Rollback()

	created := make([]menu.Item, 0, len(newItems))
	for _, newItem := range newItems {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO menu_items
			    (id, category_id, name, description, item_type, image_url,
			     price_cents, currency, quantity_available, sort_order)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
			newItem.ID, newItem.CategoryID, newItem.Name, newItem.Description,
			newItem.Type, newItem.ImageURL, newItem.PriceCents, newItem.Currency,
			newItem.Quantity, newItem.SortOrder,
		)
		if err != nil {
			return nil, menuItemWriteError(err)
		}

		item := menu.Item{
			ID: newItem.ID, Name: newItem.Name, Description: newItem.Description,
			Type: newItem.Type, ImageURL: newItem.ImageURL, PriceCents: newItem.PriceCents,
			Currency: newItem.Currency, Available: newItem.Quantity > 0,
			QuantityAvailable: newItem.Quantity, ModifierGroups: []menu.ModifierGroup{},
		}
		for _, newGroup := range newItem.ModifierGroups {
			_, err := tx.ExecContext(ctx, `
				INSERT INTO modifier_groups
				    (id, item_id, name, min_selections, max_selections, sort_order)
				VALUES ($1, $2, $3, $4, $5, $6)`,
				newGroup.ID, newItem.ID, newGroup.Name, newGroup.MinSelections,
				newGroup.MaxSelections, newGroup.SortOrder,
			)
			if err != nil {
				return nil, menuItemWriteError(err)
			}
			group := menu.ModifierGroup{
				ID: newGroup.ID, Name: newGroup.Name, MinSelections: newGroup.MinSelections,
				MaxSelections: newGroup.MaxSelections, Options: []menu.ModifierOption{},
			}
			for _, newOption := range newGroup.Options {
				available := true
				if newOption.Available != nil {
					available = *newOption.Available
				}
				_, err := tx.ExecContext(ctx, `
					INSERT INTO modifier_options
					    (id, group_id, name, price_delta_cents, is_available, sort_order)
					VALUES ($1, $2, $3, $4, $5, $6)`,
					newOption.ID, newGroup.ID, newOption.Name, newOption.PriceDeltaCents,
					available, newOption.SortOrder,
				)
				if err != nil {
					return nil, menuItemWriteError(err)
				}
				group.Options = append(group.Options, menu.ModifierOption{
					ID: newOption.ID, Name: newOption.Name,
					PriceDeltaCents: newOption.PriceDeltaCents, Available: available,
				})
			}
			item.ModifierGroups = append(item.ModifierGroups, group)
		}
		created = append(created, item)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit menu items: %w", err)
	}
	return created, nil
}

func menuItemWriteError(err error) error {
	var pgError *pgconn.PgError
	if errors.As(err, &pgError) {
		switch pgError.Code {
		case "23505":
			return ErrConflict
		case "23503":
			return ErrInvalidCategory
		}
	}
	return fmt.Errorf("create menu item: %w", err)
}

func (s *service) CreateOrder(ctx context.Context, request menu.SubmitOrderRequest, quote menu.Quote) (menu.Order, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return menu.Order{}, fmt.Errorf("begin order transaction: %w", err)
	}
	defer tx.Rollback()

	orderID, err := randomHex(16)
	if err != nil {
		return menu.Order{}, err
	}
	orderCode, err := randomHex(3)
	if err != nil {
		return menu.Order{}, err
	}
	order := menu.Order{
		ID: orderID, OrderNumber: "VL-" + strings.ToUpper(orderCode),
		CustomerName: request.CustomerName, Notes: request.Notes, Status: "submitted",
		Currency: quote.Currency, SubtotalCents: quote.SubtotalCents, Items: quote.Items,
	}
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO customer_orders
		    (id, order_number, customer_name, notes, status, currency, subtotal_cents)
		VALUES ($1, $2, $3, $4, 'submitted', $5, $6)
		RETURNING created_at`,
		order.ID, order.OrderNumber, order.CustomerName, order.Notes,
		order.Currency, order.SubtotalCents,
	).Scan(&order.CreatedAt); err != nil {
		return menu.Order{}, fmt.Errorf("insert order: %w", err)
	}

	for _, item := range quote.Items {
		var lineID int64
		if err := tx.QueryRowContext(ctx, `
			INSERT INTO customer_order_items
			    (order_id, item_id, item_name, quantity, unit_price_cents, line_total_cents)
			VALUES ($1, $2, $3, $4, $5, $6)
			RETURNING id`,
			order.ID, item.ItemID, item.Name, item.Quantity,
			item.UnitPriceCents, item.LineTotalCents,
		).Scan(&lineID); err != nil {
			return menu.Order{}, fmt.Errorf("insert order item: %w", err)
		}
		for _, option := range item.Options {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO customer_order_item_options
				    (order_item_id, option_id, option_name, price_delta_cents)
				VALUES ($1, $2, $3, $4)`,
				lineID, option.ID, option.Name, option.PriceDeltaCents,
			); err != nil {
				return menu.Order{}, fmt.Errorf("insert order option: %w", err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return menu.Order{}, fmt.Errorf("commit order: %w", err)
	}
	return order, nil
}

func (s *service) Orders(ctx context.Context, status string) ([]menu.Order, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, order_number, customer_name, notes, status, currency,
		       subtotal_cents, created_at, sold_at
		FROM customer_orders
		WHERE ($1 = '' OR status = $1)
		ORDER BY created_at DESC
		LIMIT 200`, status)
	if err != nil {
		return nil, fmt.Errorf("query orders: %w", err)
	}
	defer rows.Close()

	orders := make([]menu.Order, 0)
	for rows.Next() {
		var order menu.Order
		var soldAt sql.NullTime
		if err := rows.Scan(
			&order.ID, &order.OrderNumber, &order.CustomerName, &order.Notes,
			&order.Status, &order.Currency, &order.SubtotalCents,
			&order.CreatedAt, &soldAt,
		); err != nil {
			return nil, fmt.Errorf("scan order: %w", err)
		}
		if soldAt.Valid {
			order.SoldAt = &soldAt.Time
		}
		if err := s.loadOrderItems(ctx, &order); err != nil {
			return nil, err
		}
		orders = append(orders, order)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate orders: %w", err)
	}
	return orders, nil
}

func (s *service) UpdateOrderStatus(ctx context.Context, orderID, status string) (menu.Order, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return menu.Order{}, fmt.Errorf("begin status transaction: %w", err)
	}
	defer tx.Rollback()

	var currentStatus string
	if err := tx.QueryRowContext(ctx, `
		SELECT status FROM customer_orders WHERE id = $1 FOR UPDATE`, orderID,
	).Scan(&currentStatus); err != nil {
		if err == sql.ErrNoRows {
			return menu.Order{}, ErrNotFound
		}
		return menu.Order{}, fmt.Errorf("lock order: %w", err)
	}
	if currentStatus == status {
		if err := tx.Commit(); err != nil {
			return menu.Order{}, fmt.Errorf("commit unchanged status: %w", err)
		}
		return s.orderByID(ctx, orderID)
	}
	if currentStatus != "submitted" || (status != "sold" && status != "cancelled") {
		return menu.Order{}, ErrInvalidTransition
	}

	if status == "sold" {
		rows, err := tx.QueryContext(ctx, `
			SELECT item_id, SUM(quantity) FROM customer_order_items
			WHERE order_id = $1 GROUP BY item_id`, orderID)
		if err != nil {
			return menu.Order{}, fmt.Errorf("query order quantities: %w", err)
		}
		type quantity struct {
			itemID string
			count  int
		}
		quantities := make([]quantity, 0)
		for rows.Next() {
			var value quantity
			if err := rows.Scan(&value.itemID, &value.count); err != nil {
				rows.Close()
				return menu.Order{}, fmt.Errorf("scan order quantity: %w", err)
			}
			quantities = append(quantities, value)
		}
		if err := rows.Close(); err != nil {
			return menu.Order{}, fmt.Errorf("close order quantities: %w", err)
		}
		for _, value := range quantities {
			result, err := tx.ExecContext(ctx, `
				UPDATE menu_items
				SET quantity_available = quantity_available - $2, updated_at = NOW()
				WHERE id = $1 AND quantity_available >= $2`, value.itemID, value.count)
			if err != nil {
				return menu.Order{}, fmt.Errorf("decrement sold inventory: %w", err)
			}
			affected, _ := result.RowsAffected()
			if affected != 1 {
				return menu.Order{}, ErrInsufficientStock
			}
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE customer_orders SET status = 'sold', sold_at = NOW(), updated_at = NOW()
			WHERE id = $1`, orderID); err != nil {
			return menu.Order{}, fmt.Errorf("mark order sold: %w", err)
		}
	} else {
		if _, err := tx.ExecContext(ctx, `
			UPDATE customer_orders SET status = 'cancelled', updated_at = NOW()
			WHERE id = $1`, orderID); err != nil {
			return menu.Order{}, fmt.Errorf("cancel order: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return menu.Order{}, fmt.Errorf("commit order status: %w", err)
	}
	return s.orderByID(ctx, orderID)
}

func (s *service) orderByID(ctx context.Context, orderID string) (menu.Order, error) {
	var order menu.Order
	var soldAt sql.NullTime
	if err := s.db.QueryRowContext(ctx, `
		SELECT id, order_number, customer_name, notes, status, currency,
		       subtotal_cents, created_at, sold_at
		FROM customer_orders WHERE id = $1`, orderID).Scan(
		&order.ID, &order.OrderNumber, &order.CustomerName, &order.Notes,
		&order.Status, &order.Currency, &order.SubtotalCents,
		&order.CreatedAt, &soldAt,
	); err != nil {
		if err == sql.ErrNoRows {
			return menu.Order{}, ErrNotFound
		}
		return menu.Order{}, fmt.Errorf("query order: %w", err)
	}
	if soldAt.Valid {
		order.SoldAt = &soldAt.Time
	}
	if err := s.loadOrderItems(ctx, &order); err != nil {
		return menu.Order{}, err
	}
	return order, nil
}

func (s *service) loadOrderItems(ctx context.Context, order *menu.Order) error {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, item_id, item_name, quantity, unit_price_cents, line_total_cents
		FROM customer_order_items WHERE order_id = $1 ORDER BY id`, order.ID)
	if err != nil {
		return fmt.Errorf("query order items: %w", err)
	}
	defer rows.Close()
	order.Items = []menu.QuoteLineItem{}
	for rows.Next() {
		var lineID int64
		var item menu.QuoteLineItem
		if err := rows.Scan(
			&lineID, &item.ItemID, &item.Name, &item.Quantity,
			&item.UnitPriceCents, &item.LineTotalCents,
		); err != nil {
			return fmt.Errorf("scan order item: %w", err)
		}
		optionRows, err := s.db.QueryContext(ctx, `
			SELECT option_id, option_name, price_delta_cents
			FROM customer_order_item_options WHERE order_item_id = $1 ORDER BY id`, lineID)
		if err != nil {
			return fmt.Errorf("query order options: %w", err)
		}
		item.Options = []menu.SelectedQuoteOption{}
		for optionRows.Next() {
			var option menu.SelectedQuoteOption
			if err := optionRows.Scan(&option.ID, &option.Name, &option.PriceDeltaCents); err != nil {
				optionRows.Close()
				return fmt.Errorf("scan order option: %w", err)
			}
			item.Options = append(item.Options, option)
		}
		if err := optionRows.Close(); err != nil {
			return fmt.Errorf("close order options: %w", err)
		}
		order.Items = append(order.Items, item)
	}
	return rows.Err()
}

func (s *service) MenuQR(ctx context.Context) (dub.Link, error) {
	var link dub.Link
	if err := s.db.QueryRowContext(ctx, `
		SELECT dub_link_id, short_link, qr_code, destination, created_at
		FROM menu_qr_link WHERE singleton = TRUE`).Scan(
		&link.ID, &link.ShortLink, &link.QRCode, &link.Destination, &link.CreatedAt,
	); err != nil {
		if err == sql.ErrNoRows {
			return dub.Link{}, ErrNotFound
		}
		return dub.Link{}, fmt.Errorf("query menu QR link: %w", err)
	}
	return link, nil
}

func (s *service) SaveMenuQR(ctx context.Context, link dub.Link) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO menu_qr_link
		    (singleton, dub_link_id, short_link, qr_code, destination, created_at)
		VALUES (TRUE, $1, $2, $3, $4, $5)
		ON CONFLICT (singleton) DO UPDATE SET
		    dub_link_id = EXCLUDED.dub_link_id,
		    short_link = EXCLUDED.short_link,
		    qr_code = EXCLUDED.qr_code,
		    destination = EXCLUDED.destination,
		    created_at = EXCLUDED.created_at`,
		link.ID, link.ShortLink, link.QRCode, link.Destination, link.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("save menu QR link: %w", err)
	}
	return nil
}

func randomHex(byteCount int) (string, error) {
	value := make([]byte, byteCount)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate identifier: %w", err)
	}
	return hex.EncodeToString(value), nil
}

func (s *service) Close() error {
	return s.db.Close()
}

func envOrDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

const schemaSQL = `
CREATE TABLE IF NOT EXISTS categories (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    sort_order INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS menu_items (
    id TEXT PRIMARY KEY,
    category_id TEXT NOT NULL REFERENCES categories(id),
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    item_type TEXT NOT NULL CHECK (item_type IN ('pastry', 'cake', 'sweet', 'drink')),
    image_url TEXT NOT NULL DEFAULT '',
    price_cents INTEGER NOT NULL CHECK (price_cents >= 0),
    currency CHAR(3) NOT NULL DEFAULT 'USD',
    quantity_available INTEGER NOT NULL DEFAULT 0 CHECK (quantity_available >= 0),
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    sort_order INTEGER NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Upgrade databases created before cakes and sweets were added.
ALTER TABLE menu_items DROP CONSTRAINT IF EXISTS menu_items_item_type_check;
ALTER TABLE menu_items ADD CONSTRAINT menu_items_item_type_check
    CHECK (item_type IN ('pastry', 'cake', 'sweet', 'drink'));

CREATE TABLE IF NOT EXISTS modifier_groups (
    id TEXT PRIMARY KEY,
    item_id TEXT NOT NULL REFERENCES menu_items(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    min_selections INTEGER NOT NULL DEFAULT 0 CHECK (min_selections >= 0),
    max_selections INTEGER NOT NULL DEFAULT 1 CHECK (max_selections >= min_selections),
    sort_order INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS modifier_options (
    id TEXT PRIMARY KEY,
    group_id TEXT NOT NULL REFERENCES modifier_groups(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    price_delta_cents INTEGER NOT NULL DEFAULT 0,
    is_available BOOLEAN NOT NULL DEFAULT TRUE,
    sort_order INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS customer_orders (
    id TEXT PRIMARY KEY,
    order_number TEXT NOT NULL UNIQUE,
    customer_name TEXT NOT NULL,
    notes TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'submitted'
        CHECK (status IN ('submitted', 'sold', 'cancelled')),
    currency CHAR(3) NOT NULL,
    subtotal_cents INTEGER NOT NULL CHECK (subtotal_cents >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    sold_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS customer_order_items (
    id BIGSERIAL PRIMARY KEY,
    order_id TEXT NOT NULL REFERENCES customer_orders(id) ON DELETE CASCADE,
    item_id TEXT NOT NULL REFERENCES menu_items(id),
    item_name TEXT NOT NULL,
    quantity INTEGER NOT NULL CHECK (quantity > 0),
    unit_price_cents INTEGER NOT NULL CHECK (unit_price_cents >= 0),
    line_total_cents INTEGER NOT NULL CHECK (line_total_cents >= 0)
);

CREATE TABLE IF NOT EXISTS customer_order_item_options (
    id BIGSERIAL PRIMARY KEY,
    order_item_id BIGINT NOT NULL REFERENCES customer_order_items(id) ON DELETE CASCADE,
    option_id TEXT NOT NULL,
    option_name TEXT NOT NULL,
    price_delta_cents INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS menu_qr_link (
    singleton BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (singleton),
    dub_link_id TEXT NOT NULL,
    short_link TEXT NOT NULL,
    qr_code TEXT NOT NULL,
    destination TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL
);

INSERT INTO categories (id, name, description, sort_order) VALUES
    ('pastries', 'Fresh Pastries', 'Baked fresh for today''s display case.', 10),
    ('cakes', 'Cakes', 'Slices of the bakery''s daily cakes.', 20),
    ('sweets', 'Sweet Treats', 'Cookies and small bites for something sweet.', 30),
    ('drinks', 'Coffee & Tea', 'Coffee and tea prepared to order.', 40)
ON CONFLICT (id) DO UPDATE SET
    name = EXCLUDED.name,
    description = EXCLUDED.description,
    sort_order = EXCLUDED.sort_order;

INSERT INTO menu_items
    (id, category_id, name, description, item_type, price_cents, currency, quantity_available, sort_order)
VALUES
    ('butter-croissant', 'pastries', 'Butter Croissant', 'Flaky, deeply browned, and made with cultured butter.', 'pastry', 475, 'USD', 12, 10),
    ('pain-au-chocolat', 'pastries', 'Pain au Chocolat', 'Laminated pastry with dark chocolate batons.', 'pastry', 525, 'USD', 8, 20),
    ('seasonal-danish', 'pastries', 'Seasonal Danish', 'Buttery pastry with the bakery''s seasonal fruit.', 'pastry', 575, 'USD', 6, 30),
    ('chocolate-cake-slice', 'cakes', 'Chocolate Cake Slice', 'Dark chocolate cake with silky chocolate ganache.', 'cake', 775, 'USD', 5, 10),
    ('lemon-cake-slice', 'cakes', 'Lemon Olive Oil Cake', 'Tender lemon cake with a light citrus glaze.', 'cake', 725, 'USD', 6, 20),
    ('brown-butter-cookie', 'sweets', 'Brown Butter Cookie', 'Chewy chocolate chip cookie finished with sea salt.', 'sweet', 425, 'USD', 14, 10),
    ('pistachio-financier', 'sweets', 'Pistachio Financier', 'Small almond cake with roasted pistachio.', 'sweet', 450, 'USD', 10, 20),
    ('latte', 'drinks', 'Latte', 'Double espresso with textured milk.', 'drink', 550, 'USD', 100, 10),
    ('americano', 'drinks', 'Americano', 'Double espresso lengthened with hot water.', 'drink', 425, 'USD', 100, 20),
    ('tea', 'drinks', 'Tea', 'A rotating selection of loose-leaf tea.', 'drink', 400, 'USD', 100, 30)
ON CONFLICT (id) DO NOTHING;

INSERT INTO modifier_groups (id, item_id, name, min_selections, max_selections, sort_order) VALUES
    ('latte-size', 'latte', 'Size', 1, 1, 10),
    ('latte-milk', 'latte', 'Milk', 1, 1, 20),
    ('latte-extras', 'latte', 'Extras', 0, 2, 30),
    ('americano-size', 'americano', 'Size', 1, 1, 10),
    ('tea-style', 'tea', 'Style', 1, 1, 10)
ON CONFLICT (id) DO NOTHING;

INSERT INTO modifier_options (id, group_id, name, price_delta_cents, sort_order) VALUES
    ('latte-8oz', 'latte-size', '8 oz', 0, 10),
    ('latte-12oz', 'latte-size', '12 oz', 75, 20),
    ('latte-whole', 'latte-milk', 'Whole milk', 0, 10),
    ('latte-oat', 'latte-milk', 'Oat milk', 75, 20),
    ('latte-extra-shot', 'latte-extras', 'Extra shot', 125, 10),
    ('latte-vanilla', 'latte-extras', 'Vanilla', 75, 20),
    ('americano-8oz', 'americano-size', '8 oz', 0, 10),
    ('americano-12oz', 'americano-size', '12 oz', 50, 20),
    ('tea-hot', 'tea-style', 'Hot', 0, 10),
    ('tea-iced', 'tea-style', 'Iced', 50, 20)
ON CONFLICT (id) DO NOTHING;
`
