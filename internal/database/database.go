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

	"github.com/shohinx/vanilla-api/internal/sdk/models"
)

type Service interface {
	Initialize(context.Context) error
	Health(context.Context) map[string]string
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
	Close() error
}

type service struct {
	db *sql.DB
}

var (
	ErrNotFound          = errors.New("not found")
	ErrInsufficientStock = errors.New("insufficient stock")
	ErrInvalidTransition = errors.New("invalid order status transition")
	ErrConflict          = errors.New("resource already exists")
	ErrInvalidCategory   = errors.New("category does not exist")
	ErrStockNotTracked   = errors.New("stock is not tracked for this item")
	ErrInactiveStaff     = errors.New("staff member does not exist or is inactive")
)

func New() Service {
	db, _ := sql.Open("pgx", databaseConnectionURL())
	db.SetMaxOpenConns(20)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(30 * time.Minute)
	return &service{db: db}
}

func databaseConnectionURL() string {
	sslMode := envOrDefault("BLUEPRINT_DB_SSLMODE", "disable")
	schema := envOrDefault("BLUEPRINT_DB_SCHEMA", "public")

	if rawURL := strings.TrimSpace(os.Getenv("DATABASE_URL")); rawURL != "" {
		connection, err := url.Parse(rawURL)
		if err != nil {
			return rawURL
		}
		query := connection.Query()
		if !query.Has("sslmode") {
			query.Set("sslmode", sslMode)
		}
		if !query.Has("search_path") {
			query.Set("search_path", schema)
		}
		connection.RawQuery = query.Encode()
		return connection.String()
	}

	connection := &url.URL{
		Scheme: "postgres",
		User: url.UserPassword(
			envOrDefault("BLUEPRINT_DB_USERNAME", "postgres"),
			envOrDefault("BLUEPRINT_DB_PASSWORD", "postgres"),
		),
		Host: envOrDefault("BLUEPRINT_DB_HOST", "localhost") + ":" + envOrDefault("BLUEPRINT_DB_PORT", "5432"),
		Path: envOrDefault("BLUEPRINT_DB_DATABASE", "vanilla_api"),
	}
	query := connection.Query()
	query.Set("sslmode", sslMode)
	query.Set("search_path", schema)
	connection.RawQuery = query.Encode()
	return connection.String()
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
	if err := s.db.PingContext(ctx); err != nil {
		return map[string]string{"status": "down", "error": err.Error()}
	}
	stats := s.db.Stats()
	return map[string]string{
		"status": "up", "message": "database is healthy",
		"open_connections": strconv.Itoa(stats.OpenConnections),
		"in_use":           strconv.Itoa(stats.InUse), "idle": strconv.Itoa(stats.Idle),
	}
}

func (s *service) Menu(ctx context.Context, includeUnavailable bool) (models.Menu, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT c.id, c.name, c.sort_order,
		       i.id, i.category_id, i.name, i.description, COALESCE(i.image_url, ''),
		       i.price_cents, i.available, i.track_stock, i.stock_qty
		FROM category c
		JOIN menu_item i ON i.category_id = c.id
		WHERE $1 OR (i.available AND (NOT i.track_stock OR i.stock_qty > 0))
		ORDER BY c.sort_order, c.name, i.sort_order, i.name`, includeUnavailable)
	if err != nil {
		return models.Menu{}, fmt.Errorf("query menu items: %w", err)
	}
	defer rows.Close()

	type categoryData struct {
		category models.Category
		itemIDs  []int64
	}
	categories := make([]categoryData, 0)
	categoryIndexes := make(map[int64]int)
	items := make(map[int64]*models.Item)
	for rows.Next() {
		var categoryID int64
		var categoryName string
		var categorySort int
		var manualAvailable bool
		var stock sql.NullInt64
		item := &models.Item{VariantGroups: []models.VariantGroup{}}
		if err := rows.Scan(
			&categoryID, &categoryName, &categorySort,
			&item.ID, &item.CategoryID, &item.Name, &item.Description, &item.ImageURL,
			&item.PriceCents, &manualAvailable, &item.TrackStock, &stock,
		); err != nil {
			return models.Menu{}, fmt.Errorf("scan menu item: %w", err)
		}
		item.Available = manualAvailable && (!item.TrackStock || (stock.Valid && stock.Int64 > 0))
		if stock.Valid {
			quantity := int(stock.Int64)
			item.StockQuantity = &quantity
		}
		items[item.ID] = item
		categoryIndex, found := categoryIndexes[categoryID]
		if !found {
			categoryIndex = len(categories)
			categoryIndexes[categoryID] = categoryIndex
			categories = append(categories, categoryData{category: models.Category{
				ID: categoryID, Name: categoryName, SortOrder: categorySort, Items: []models.Item{},
			}})
		}
		categories[categoryIndex].itemIDs = append(categories[categoryIndex].itemIDs, item.ID)
	}
	if err := rows.Err(); err != nil {
		return models.Menu{}, fmt.Errorf("iterate menu items: %w", err)
	}

	variantRows, err := s.db.QueryContext(ctx, `
		SELECT vg.id, vg.menu_item_id, vg.name, vg.required,
		       vo.id, vo.name, vo.price_cents, vo.track_stock, vo.stock_qty
		FROM variant_group vg
		JOIN variant_option vo ON vo.variant_group_id = vg.id
		ORDER BY vg.id, vo.sort_order, vo.name`)
	if err != nil {
		return models.Menu{}, fmt.Errorf("query variants: %w", err)
	}
	defer variantRows.Close()
	groupIndexes := make(map[int64]int)
	for variantRows.Next() {
		var groupID, itemID int64
		var groupName string
		var required bool
		var option models.VariantOption
		var stock sql.NullInt64
		if err := variantRows.Scan(
			&groupID, &itemID, &groupName, &required,
			&option.ID, &option.Name, &option.PriceCents, &option.TrackStock, &stock,
		); err != nil {
			return models.Menu{}, fmt.Errorf("scan variant: %w", err)
		}
		item, found := items[itemID]
		if !found {
			continue
		}
		option.Available = !option.TrackStock || (stock.Valid && stock.Int64 > 0)
		if stock.Valid {
			quantity := int(stock.Int64)
			option.StockQuantity = &quantity
		}
		groupIndex, found := groupIndexes[groupID]
		if !found {
			groupIndex = len(item.VariantGroups)
			groupIndexes[groupID] = groupIndex
			item.VariantGroups = append(item.VariantGroups, models.VariantGroup{
				ID: groupID, Name: groupName, Required: required, Options: []models.VariantOption{},
			})
		}
		item.VariantGroups[groupIndex].Options = append(item.VariantGroups[groupIndex].Options, option)
	}
	if err := variantRows.Err(); err != nil {
		return models.Menu{}, fmt.Errorf("iterate variants: %w", err)
	}

	result := models.Menu{GeneratedAt: time.Now().UTC(), Categories: []models.Category{}}
	for _, data := range categories {
		for _, itemID := range data.itemIDs {
			item := items[itemID]
			if len(item.VariantGroups) > 0 {
				hasAvailableVariant := false
				for _, option := range item.VariantGroups[0].Options {
					if option.Available {
						hasAvailableVariant = true
						break
					}
				}
				item.Available = item.Available && hasAvailableVariant
			}
			if includeUnavailable || item.Available {
				data.category.Items = append(data.category.Items, *item)
			}
		}
		if includeUnavailable || len(data.category.Items) > 0 {
			result.Categories = append(result.Categories, data.category)
		}
	}
	return result, nil
}

func (s *service) Categories(ctx context.Context) ([]models.MenuCategory, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, sort_order FROM category ORDER BY sort_order, name`)
	if err != nil {
		return nil, fmt.Errorf("query categories: %w", err)
	}
	defer rows.Close()
	result := []models.MenuCategory{}
	for rows.Next() {
		var category models.MenuCategory
		if err := rows.Scan(&category.ID, &category.Name, &category.SortOrder); err != nil {
			return nil, fmt.Errorf("scan category: %w", err)
		}
		result = append(result, category)
	}
	return result, rows.Err()
}

func (s *service) CreateCategories(ctx context.Context, categories []models.NewCategory) ([]models.MenuCategory, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin category transaction: %w", err)
	}
	defer tx.Rollback()
	created := make([]models.MenuCategory, 0, len(categories))
	for _, category := range categories {
		var value models.MenuCategory
		err := tx.QueryRowContext(ctx, `
			INSERT INTO category (name, sort_order) VALUES ($1, $2)
			RETURNING id, name, sort_order`, category.Name, category.SortOrder,
		).Scan(&value.ID, &value.Name, &value.SortOrder)
		if err != nil {
			return nil, writeError(err)
		}
		created = append(created, value)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit categories: %w", err)
	}
	return created, nil
}

func (s *service) UpdateInventory(ctx context.Context, itemID int64, quantity int) (models.Inventory, error) {
	var inventory models.Inventory
	err := s.db.QueryRowContext(ctx, `
		UPDATE menu_item SET stock_qty = $2, updated_at = NOW()
		WHERE id = $1 AND track_stock
		RETURNING id, stock_qty, available, updated_at`, itemID, quantity,
	).Scan(&inventory.ItemID, &inventory.Quantity, &inventory.Available, &inventory.UpdatedAt)
	if err == sql.ErrNoRows {
		var exists bool
		if checkErr := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM menu_item WHERE id = $1)`, itemID).Scan(&exists); checkErr != nil {
			return models.Inventory{}, fmt.Errorf("check menu item: %w", checkErr)
		}
		if exists {
			return models.Inventory{}, ErrStockNotTracked
		}
		return models.Inventory{}, ErrNotFound
	}
	if err != nil {
		return models.Inventory{}, fmt.Errorf("update inventory: %w", err)
	}
	inventory.Available = inventory.Available && inventory.Quantity > 0
	return inventory, nil
}

func (s *service) UpdateVariantInventory(ctx context.Context, variantOptionID int64, quantity int) (models.VariantInventory, error) {
	var inventory models.VariantInventory
	err := s.db.QueryRowContext(ctx, `
		UPDATE variant_option SET stock_qty = $2
		WHERE id = $1 AND track_stock
		RETURNING id, stock_qty`, variantOptionID, quantity,
	).Scan(&inventory.VariantOptionID, &inventory.Quantity)
	if err == sql.ErrNoRows {
		var exists bool
		if checkErr := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM variant_option WHERE id = $1)`, variantOptionID).Scan(&exists); checkErr != nil {
			return models.VariantInventory{}, fmt.Errorf("check variant option: %w", checkErr)
		}
		if exists {
			return models.VariantInventory{}, ErrStockNotTracked
		}
		return models.VariantInventory{}, ErrNotFound
	}
	if err != nil {
		return models.VariantInventory{}, fmt.Errorf("update variant inventory: %w", err)
	}
	inventory.Available = inventory.Quantity > 0
	return inventory, nil
}

func (s *service) UpdateItemAvailability(ctx context.Context, itemID int64, available bool) (models.ItemAvailability, error) {
	var availability models.ItemAvailability
	err := s.db.QueryRowContext(ctx, `
		UPDATE menu_item SET available = $2, updated_at = NOW()
		WHERE id = $1 RETURNING id, available, updated_at`, itemID, available,
	).Scan(&availability.ItemID, &availability.Available, &availability.UpdatedAt)
	if err == sql.ErrNoRows {
		return models.ItemAvailability{}, ErrNotFound
	}
	if err != nil {
		return models.ItemAvailability{}, fmt.Errorf("update item availability: %w", err)
	}
	return availability, nil
}

func (s *service) UpdateItemImage(ctx context.Context, itemID int64, imageURL string) (models.ItemImage, error) {
	var image models.ItemImage
	err := s.db.QueryRowContext(ctx, `
		UPDATE menu_item SET image_url = NULLIF($2, ''), updated_at = NOW()
		WHERE id = $1
		RETURNING id, COALESCE(image_url, ''), updated_at`, itemID, imageURL,
	).Scan(&image.ItemID, &image.ImageURL, &image.UpdatedAt)
	if err == sql.ErrNoRows {
		return models.ItemImage{}, ErrNotFound
	}
	if err != nil {
		return models.ItemImage{}, fmt.Errorf("update item image: %w", err)
	}
	return image, nil
}

func (s *service) CreateMenuItems(ctx context.Context, newItems []models.NewItem) ([]models.Item, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin menu item transaction: %w", err)
	}
	defer tx.Rollback()
	created := make([]models.Item, 0, len(newItems))
	for _, newItem := range newItems {
		available := true
		if newItem.Available != nil {
			available = *newItem.Available
		}
		var imageURL any
		if newItem.ImageURL != nil && *newItem.ImageURL != "" {
			imageURL = *newItem.ImageURL
		}
		var item models.Item
		var stock sql.NullInt64
		err := tx.QueryRowContext(ctx, `
			INSERT INTO menu_item
			    (category_id, name, description, price_cents, track_stock, stock_qty, available, image_url, sort_order)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
			RETURNING id, category_id, name, description, COALESCE(image_url, ''), price_cents,
			          available, track_stock, stock_qty`,
			newItem.CategoryID, newItem.Name, newItem.Description, newItem.PriceCents,
			newItem.TrackStock, newItem.StockQuantity, available, imageURL, newItem.SortOrder,
		).Scan(&item.ID, &item.CategoryID, &item.Name, &item.Description, &item.ImageURL,
			&item.PriceCents, &item.Available, &item.TrackStock, &stock)
		if err != nil {
			return nil, writeError(err)
		}
		if stock.Valid {
			quantity := int(stock.Int64)
			item.StockQuantity = &quantity
			item.Available = item.Available && quantity > 0
		}
		item.VariantGroups = []models.VariantGroup{}
		if newItem.VariantGroup != nil {
			group := models.VariantGroup{Name: newItem.VariantGroup.Name, Required: true, Options: []models.VariantOption{}}
			if err := tx.QueryRowContext(ctx, `
				INSERT INTO variant_group (menu_item_id, name, required)
				VALUES ($1, $2, TRUE) RETURNING id`, item.ID, group.Name,
			).Scan(&group.ID); err != nil {
				return nil, writeError(err)
			}
			for optionIndex, newOption := range newItem.VariantGroup.Options {
				var option models.VariantOption
				var optionStock sql.NullInt64
				err := tx.QueryRowContext(ctx, `
					INSERT INTO variant_option
					    (variant_group_id, name, price_cents, track_stock, stock_qty, sort_order)
					VALUES ($1, $2, $3, $4, $5, $6)
					RETURNING id, name, price_cents, track_stock, stock_qty`,
					group.ID, newOption.Name, newOption.PriceCents, newOption.TrackStock,
					newOption.StockQuantity, optionIndex,
				).Scan(&option.ID, &option.Name, &option.PriceCents, &option.TrackStock, &optionStock)
				if err != nil {
					return nil, writeError(err)
				}
				option.Available = true
				if optionStock.Valid {
					quantity := int(optionStock.Int64)
					option.StockQuantity = &quantity
					option.Available = quantity > 0
				}
				group.Options = append(group.Options, option)
			}
			item.VariantGroups = append(item.VariantGroups, group)
		}
		created = append(created, item)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit menu items: %w", err)
	}
	return created, nil
}

func writeError(err error) error {
	var pgError *pgconn.PgError
	if errors.As(err, &pgError) {
		switch pgError.Code {
		case "23505":
			return ErrConflict
		case "23503":
			return ErrInvalidCategory
		}
	}
	return fmt.Errorf("write database record: %w", err)
}

func (s *service) CreateOrder(ctx context.Context, request models.SubmitOrderRequest, quote models.Quote) (models.Order, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return models.Order{}, fmt.Errorf("begin order transaction: %w", err)
	}
	defer tx.Rollback()
	orderCode, err := randomHex(3)
	if err != nil {
		return models.Order{}, err
	}
	order := models.Order{
		OrderNumber: "VL-" + strings.ToUpper(orderCode), TableNumber: request.TableNumber,
		GuestCount: request.GuestCount, Status: "new", TotalCents: quote.TotalCents, Items: quote.Items,
	}
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO orders (order_number, table_number, guest_count, status)
		VALUES ($1, $2, $3, 'new') RETURNING id, created_at`,
		order.OrderNumber, order.TableNumber, order.GuestCount,
	).Scan(&order.ID, &order.CreatedAt); err != nil {
		return models.Order{}, fmt.Errorf("insert order: %w", err)
	}
	for _, item := range quote.Items {
		var variantID any
		if item.Variant != nil {
			variantID = item.Variant.ID
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO order_item
			    (order_id, menu_item_id, variant_option_id, quantity, unit_price_cents)
			VALUES ($1, $2, $3, $4, $5)`,
			order.ID, item.ItemID, variantID, item.Quantity, item.UnitPriceCents,
		); err != nil {
			return models.Order{}, fmt.Errorf("insert order item: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return models.Order{}, fmt.Errorf("commit order: %w", err)
	}
	return order, nil
}

func (s *service) Orders(ctx context.Context, status string) ([]models.Order, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT o.id, o.order_number, o.staff_id, COALESCE(s.name, ''), o.table_number,
		       o.guest_count, o.status, o.created_at, o.sold_at
		FROM orders o LEFT JOIN staff s ON s.id = o.staff_id
		WHERE ($1 = '' OR o.status = $1)
		ORDER BY o.created_at DESC LIMIT 200`, status)
	if err != nil {
		return nil, fmt.Errorf("query orders: %w", err)
	}
	defer rows.Close()
	result := []models.Order{}
	for rows.Next() {
		order, err := scanOrder(rows)
		if err != nil {
			return nil, err
		}
		if err := s.loadOrderItems(ctx, &order); err != nil {
			return nil, err
		}
		result = append(result, order)
	}
	return result, rows.Err()
}

type rowScanner interface {
	Scan(...any) error
}

func scanOrder(row rowScanner) (models.Order, error) {
	var order models.Order
	var staffID sql.NullInt64
	var soldAt sql.NullTime
	if err := row.Scan(
		&order.ID, &order.OrderNumber, &staffID, &order.StaffName, &order.TableNumber,
		&order.GuestCount, &order.Status, &order.CreatedAt, &soldAt,
	); err != nil {
		return models.Order{}, fmt.Errorf("scan order: %w", err)
	}
	if staffID.Valid {
		value := staffID.Int64
		order.StaffID = &value
	}
	if soldAt.Valid {
		value := soldAt.Time
		order.SoldAt = &value
	}
	return order, nil
}

func (s *service) UpdateOrderStatus(ctx context.Context, orderID int64, status string, staffID int64) (models.Order, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return models.Order{}, fmt.Errorf("begin status transaction: %w", err)
	}
	defer tx.Rollback()
	var staffActive bool
	if err := tx.QueryRowContext(ctx, `SELECT active FROM staff WHERE id = $1`, staffID).Scan(&staffActive); err != nil || !staffActive {
		return models.Order{}, ErrInactiveStaff
	}
	var currentStatus string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM orders WHERE id = $1 FOR UPDATE`, orderID).Scan(&currentStatus); err != nil {
		if err == sql.ErrNoRows {
			return models.Order{}, ErrNotFound
		}
		return models.Order{}, fmt.Errorf("lock order: %w", err)
	}
	if currentStatus == status {
		if err := tx.Commit(); err != nil {
			return models.Order{}, fmt.Errorf("commit unchanged status: %w", err)
		}
		return s.orderByID(ctx, orderID)
	}
	if currentStatus != "new" || (status != "sold" && status != "cancelled") {
		return models.Order{}, ErrInvalidTransition
	}

	if status == "sold" {
		rows, err := tx.QueryContext(ctx, `
			SELECT oi.menu_item_id, oi.variant_option_id, oi.quantity,
			       mi.track_stock, COALESCE(vo.track_stock, FALSE)
			FROM order_item oi
			JOIN menu_item mi ON mi.id = oi.menu_item_id
			LEFT JOIN variant_option vo ON vo.id = oi.variant_option_id
			WHERE oi.order_id = $1`, orderID)
		if err != nil {
			return models.Order{}, fmt.Errorf("query order stock: %w", err)
		}
		itemQuantities := make(map[int64]int)
		variantQuantities := make(map[int64]int)
		for rows.Next() {
			var itemID int64
			var variantID sql.NullInt64
			var quantity int
			var itemTracks, variantTracks bool
			if err := rows.Scan(&itemID, &variantID, &quantity, &itemTracks, &variantTracks); err != nil {
				rows.Close()
				return models.Order{}, fmt.Errorf("scan order stock: %w", err)
			}
			if variantID.Valid && variantTracks {
				variantQuantities[variantID.Int64] += quantity
			} else if itemTracks {
				itemQuantities[itemID] += quantity
			}
		}
		if err := rows.Close(); err != nil {
			return models.Order{}, fmt.Errorf("close order stock rows: %w", err)
		}
		for itemID, quantity := range itemQuantities {
			result, err := tx.ExecContext(ctx, `
				UPDATE menu_item SET stock_qty = stock_qty - $2, updated_at = NOW()
				WHERE id = $1 AND track_stock AND stock_qty >= $2`, itemID, quantity)
			if err != nil {
				return models.Order{}, fmt.Errorf("decrement item stock: %w", err)
			}
			if affected, _ := result.RowsAffected(); affected != 1 {
				return models.Order{}, ErrInsufficientStock
			}
		}
		for variantID, quantity := range variantQuantities {
			result, err := tx.ExecContext(ctx, `
				UPDATE variant_option SET stock_qty = stock_qty - $2
				WHERE id = $1 AND track_stock AND stock_qty >= $2`, variantID, quantity)
			if err != nil {
				return models.Order{}, fmt.Errorf("decrement variant stock: %w", err)
			}
			if affected, _ := result.RowsAffected(); affected != 1 {
				return models.Order{}, ErrInsufficientStock
			}
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE orders SET status = 'sold', staff_id = $2, sold_at = NOW()
			WHERE id = $1`, orderID, staffID); err != nil {
			return models.Order{}, fmt.Errorf("mark order sold: %w", err)
		}
	} else if _, err := tx.ExecContext(ctx, `
		UPDATE orders SET status = 'cancelled', staff_id = $2 WHERE id = $1`, orderID, staffID); err != nil {
		return models.Order{}, fmt.Errorf("cancel order: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return models.Order{}, fmt.Errorf("commit order status: %w", err)
	}
	return s.orderByID(ctx, orderID)
}

func (s *service) orderByID(ctx context.Context, orderID int64) (models.Order, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT o.id, o.order_number, o.staff_id, COALESCE(s.name, ''), o.table_number,
		       o.guest_count, o.status, o.created_at, o.sold_at
		FROM orders o LEFT JOIN staff s ON s.id = o.staff_id WHERE o.id = $1`, orderID)
	order, err := scanOrder(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) || strings.Contains(err.Error(), sql.ErrNoRows.Error()) {
			return models.Order{}, ErrNotFound
		}
		return models.Order{}, err
	}
	if err := s.loadOrderItems(ctx, &order); err != nil {
		return models.Order{}, err
	}
	return order, nil
}

func (s *service) loadOrderItems(ctx context.Context, order *models.Order) error {
	rows, err := s.db.QueryContext(ctx, `
		SELECT mi.id, mi.name, oi.quantity, oi.unit_price_cents,
		       vo.id, COALESCE(vo.name, '')
		FROM order_item oi
		JOIN menu_item mi ON mi.id = oi.menu_item_id
		LEFT JOIN variant_option vo ON vo.id = oi.variant_option_id
		WHERE oi.order_id = $1 ORDER BY oi.id`, order.ID)
	if err != nil {
		return fmt.Errorf("query order items: %w", err)
	}
	defer rows.Close()
	order.Items = []models.QuoteLineItem{}
	order.TotalCents = 0
	for rows.Next() {
		var item models.QuoteLineItem
		var variantID sql.NullInt64
		var variantName string
		if err := rows.Scan(&item.ItemID, &item.Name, &item.Quantity, &item.UnitPriceCents, &variantID, &variantName); err != nil {
			return fmt.Errorf("scan order item: %w", err)
		}
		if variantID.Valid {
			item.Variant = &models.SelectedVariantOption{ID: variantID.Int64, Name: variantName}
		}
		item.LineTotalCents = item.UnitPriceCents * item.Quantity
		order.TotalCents += item.LineTotalCents
		order.Items = append(order.Items, item)
	}
	return rows.Err()
}

func (s *service) Staff(ctx context.Context, includeInactive bool) ([]models.Staff, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, active, created_at FROM staff
		WHERE $1 OR active ORDER BY name`, includeInactive)
	if err != nil {
		return nil, fmt.Errorf("query staff: %w", err)
	}
	defer rows.Close()
	result := []models.Staff{}
	for rows.Next() {
		var member models.Staff
		if err := rows.Scan(&member.ID, &member.Name, &member.Active, &member.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan staff: %w", err)
		}
		result = append(result, member)
	}
	return result, rows.Err()
}

func (s *service) StaffActive(ctx context.Context, staffID int64) (bool, error) {
	var active bool
	err := s.db.QueryRowContext(ctx, `SELECT active FROM staff WHERE id = $1`, staffID).Scan(&active)
	if err == sql.ErrNoRows {
		return false, ErrNotFound
	}
	if err != nil {
		return false, fmt.Errorf("query staff status: %w", err)
	}
	return active, nil
}

func (s *service) StaffCredentials(ctx context.Context, name string) (models.Staff, string, error) {
	var member models.Staff
	var pinHash string
	err := s.db.QueryRowContext(ctx, `
		SELECT id, name, pin_hash, active, created_at
		FROM staff WHERE LOWER(name) = LOWER($1)`, name,
	).Scan(&member.ID, &member.Name, &pinHash, &member.Active, &member.CreatedAt)
	if err == sql.ErrNoRows {
		return models.Staff{}, "", ErrNotFound
	}
	if err != nil {
		return models.Staff{}, "", fmt.Errorf("query staff credentials: %w", err)
	}
	return member, pinHash, nil
}

func (s *service) CreateStaff(ctx context.Context, name, pinHash string) (models.Staff, error) {
	var member models.Staff
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO staff (name, pin_hash) VALUES ($1, $2)
		RETURNING id, name, active, created_at`, name, pinHash,
	).Scan(&member.ID, &member.Name, &member.Active, &member.CreatedAt)
	if err != nil {
		return models.Staff{}, writeError(err)
	}
	return member, nil
}

func (s *service) SetStaffActive(ctx context.Context, staffID int64, active bool) (models.Staff, error) {
	var member models.Staff
	err := s.db.QueryRowContext(ctx, `
		UPDATE staff SET active = $2 WHERE id = $1
		RETURNING id, name, active, created_at`, staffID, active,
	).Scan(&member.ID, &member.Name, &member.Active, &member.CreatedAt)
	if err == sql.ErrNoRows {
		return models.Staff{}, ErrNotFound
	}
	if err != nil {
		return models.Staff{}, fmt.Errorf("update staff: %w", err)
	}
	return member, nil
}

func (s *service) MenuQR(ctx context.Context) (models.Link, error) {
	var link models.Link
	if err := s.db.QueryRowContext(ctx, `
		SELECT dub_link_id, short_link, qr_code, destination, created_at
		FROM menu_qr_link WHERE singleton = TRUE`).Scan(
		&link.ID, &link.ShortLink, &link.QRCode, &link.Destination, &link.CreatedAt,
	); err != nil {
		if err == sql.ErrNoRows {
			return models.Link{}, ErrNotFound
		}
		return models.Link{}, fmt.Errorf("query menu QR link: %w", err)
	}
	return link, nil
}

func (s *service) SaveMenuQR(ctx context.Context, link models.Link) error {
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

func (s *service) Close() error { return s.db.Close() }

func envOrDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

const schemaSQL = `
-- This project intentionally uses a clean-cut schema. Remove tables from the
-- discarded prototype instead of carrying a compatibility layer forward.
DROP TABLE IF EXISTS customer_order_item_options;
DROP TABLE IF EXISTS customer_order_items;
DROP TABLE IF EXISTS customer_orders;
DROP TABLE IF EXISTS modifier_options;
DROP TABLE IF EXISTS modifier_groups;
DROP TABLE IF EXISTS menu_items;
DROP TABLE IF EXISTS categories;

CREATE TABLE IF NOT EXISTS staff (
    id BIGSERIAL PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    pin_hash TEXT NOT NULL,
    active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS category (
    id BIGSERIAL PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    sort_order INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS menu_item (
    id BIGSERIAL PRIMARY KEY,
    category_id BIGINT NOT NULL REFERENCES category(id),
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    price_cents INTEGER NOT NULL CHECK (price_cents >= 0),
    track_stock BOOLEAN NOT NULL DEFAULT FALSE,
    stock_qty INTEGER,
    available BOOLEAN NOT NULL DEFAULT TRUE,
    image_url TEXT,
    sort_order INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (category_id, name),
    CHECK ((track_stock AND stock_qty IS NOT NULL AND stock_qty >= 0)
        OR (NOT track_stock AND stock_qty IS NULL))
);

CREATE TABLE IF NOT EXISTS variant_group (
    id BIGSERIAL PRIMARY KEY,
    menu_item_id BIGINT NOT NULL UNIQUE REFERENCES menu_item(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    required BOOLEAN NOT NULL DEFAULT TRUE CHECK (required)
);

CREATE TABLE IF NOT EXISTS variant_option (
    id BIGSERIAL PRIMARY KEY,
    variant_group_id BIGINT NOT NULL REFERENCES variant_group(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    price_cents INTEGER NOT NULL CHECK (price_cents >= 0),
    track_stock BOOLEAN NOT NULL DEFAULT FALSE,
    stock_qty INTEGER,
    sort_order INTEGER NOT NULL DEFAULT 0,
    UNIQUE (variant_group_id, name),
    CHECK ((track_stock AND stock_qty IS NOT NULL AND stock_qty >= 0)
        OR (NOT track_stock AND stock_qty IS NULL))
);

CREATE TABLE IF NOT EXISTS orders (
    id BIGSERIAL PRIMARY KEY,
    order_number TEXT NOT NULL UNIQUE,
    staff_id BIGINT REFERENCES staff(id),
    table_number TEXT NOT NULL CHECK (BTRIM(table_number) <> '' AND LENGTH(table_number) <= 30),
    guest_count INTEGER NOT NULL CHECK (guest_count BETWEEN 1 AND 100),
    status TEXT NOT NULL DEFAULT 'new' CHECK (status IN ('new', 'sold', 'cancelled')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    sold_at TIMESTAMPTZ,
    CHECK (
        (status = 'new' AND staff_id IS NULL AND sold_at IS NULL)
        OR (status = 'sold' AND staff_id IS NOT NULL AND sold_at IS NOT NULL)
        OR (status = 'cancelled' AND staff_id IS NOT NULL AND sold_at IS NULL)
    )
);

CREATE TABLE IF NOT EXISTS order_item (
    id BIGSERIAL PRIMARY KEY,
    order_id BIGINT NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
    menu_item_id BIGINT NOT NULL REFERENCES menu_item(id),
    variant_option_id BIGINT REFERENCES variant_option(id),
    quantity INTEGER NOT NULL CHECK (quantity > 0),
    unit_price_cents INTEGER NOT NULL CHECK (unit_price_cents >= 0)
);

CREATE INDEX IF NOT EXISTS orders_status_created_at_idx ON orders (status, created_at DESC);

CREATE TABLE IF NOT EXISTS menu_qr_link (
    singleton BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (singleton),
    dub_link_id TEXT NOT NULL,
    short_link TEXT NOT NULL,
    qr_code TEXT NOT NULL,
    destination TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL
);

INSERT INTO category (name, sort_order) VALUES
    ('Fresh Pastries', 10),
    ('Cakes', 20),
    ('Sweet Treats', 30),
    ('Coffee & Tea', 40)
ON CONFLICT (name) DO UPDATE SET sort_order = EXCLUDED.sort_order;

INSERT INTO menu_item
    (category_id, name, description, price_cents, track_stock, stock_qty, sort_order)
SELECT c.id, seed.name, seed.description, seed.price_cents, seed.track_stock, seed.stock_qty, seed.sort_order
FROM (VALUES
    ('Fresh Pastries', 'Butter Croissant', 'Flaky, deeply browned, and made with cultured butter.', 475, TRUE, 12, 10),
    ('Fresh Pastries', 'Pain au Chocolat', 'Laminated pastry with dark chocolate batons.', 525, TRUE, 8, 20),
    ('Fresh Pastries', 'Seasonal Danish', 'Buttery pastry with seasonal fruit.', 575, TRUE, 6, 30),
    ('Cakes', 'Chocolate Cake Slice', 'Dark chocolate cake with silky chocolate ganache.', 775, TRUE, 5, 10),
    ('Cakes', 'Lemon Olive Oil Cake', 'Tender lemon cake with a light citrus glaze.', 725, TRUE, 6, 20),
    ('Sweet Treats', 'Brown Butter Cookie', 'Chewy chocolate chip cookie finished with sea salt.', 425, TRUE, 14, 10),
    ('Sweet Treats', 'Pistachio Financier', 'Small almond cake with roasted pistachio.', 450, TRUE, 10, 20),
    ('Coffee & Tea', 'Latte', 'Double espresso with textured milk.', 550, FALSE, NULL::INTEGER, 10),
    ('Coffee & Tea', 'Americano', 'Double espresso lengthened with hot water.', 425, FALSE, NULL::INTEGER, 20),
    ('Coffee & Tea', 'Tea', 'A rotating selection of loose-leaf tea.', 400, FALSE, NULL::INTEGER, 30)
) AS seed(category_name, name, description, price_cents, track_stock, stock_qty, sort_order)
JOIN category c ON c.name = seed.category_name
ON CONFLICT (category_id, name) DO NOTHING;

INSERT INTO variant_group (menu_item_id, name, required)
SELECT i.id, seed.group_name, TRUE
FROM (VALUES
    ('Coffee & Tea', 'Latte', 'Size'),
    ('Coffee & Tea', 'Americano', 'Size'),
    ('Coffee & Tea', 'Tea', 'Style')
) AS seed(category_name, item_name, group_name)
JOIN category c ON c.name = seed.category_name
JOIN menu_item i ON i.category_id = c.id AND i.name = seed.item_name
ON CONFLICT (menu_item_id) DO NOTHING;

INSERT INTO variant_option (variant_group_id, name, price_cents, sort_order)
SELECT vg.id, seed.option_name, seed.price_cents, seed.sort_order
FROM (VALUES
    ('Coffee & Tea', 'Latte', 'Small', 550, 10),
    ('Coffee & Tea', 'Latte', 'Large', 625, 20),
    ('Coffee & Tea', 'Americano', 'Small', 425, 10),
    ('Coffee & Tea', 'Americano', 'Large', 475, 20),
    ('Coffee & Tea', 'Tea', 'Hot', 400, 10),
    ('Coffee & Tea', 'Tea', 'Iced', 450, 20)
) AS seed(category_name, item_name, option_name, price_cents, sort_order)
JOIN category c ON c.name = seed.category_name
JOIN menu_item i ON i.category_id = c.id AND i.name = seed.item_name
JOIN variant_group vg ON vg.menu_item_id = i.id
ON CONFLICT (variant_group_id, name) DO NOTHING;
`
