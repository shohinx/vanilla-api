package database

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "github.com/joho/godotenv/autoload"

	"github.com/shohinx/vanilla-api/internal/sdk/models"
)

type Store struct {
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

func New() (*Store, error) {
	connectionURL, err := databaseConnectionURL()
	if err != nil {
		return nil, err
	}
	return Open(connectionURL)
}

// Open creates a database service for an explicit PostgreSQL connection URL.
// The connection is verified by Initialize, allowing callers to control when
// startup I/O occurs.
func Open(connectionURL string) (*Store, error) {
	if strings.TrimSpace(connectionURL) == "" {
		return nil, errors.New("postgres connection URL is required")
	}
	db, err := sql.Open("pgx", connectionURL)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	db.SetMaxOpenConns(20)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(30 * time.Minute)
	db.SetConnMaxIdleTime(5 * time.Minute)
	return &Store{db: db}, nil
}

func databaseConnectionURL() (string, error) {
	sslMode := envOrDefault("BLUEPRINT_DB_SSLMODE", "disable")
	schema := envOrDefault("BLUEPRINT_DB_SCHEMA", "public")

	if rawURL := strings.TrimSpace(os.Getenv("DATABASE_URL")); rawURL != "" {
		connection, err := url.Parse(rawURL)
		if err != nil {
			return "", fmt.Errorf("parse DATABASE_URL: %w", err)
		}
		if connection.Scheme != "postgres" && connection.Scheme != "postgresql" {
			return "", errors.New("DATABASE_URL must use the postgres or postgresql scheme")
		}
		if strings.TrimPrefix(connection.Path, "/") == "" {
			return "", errors.New("DATABASE_URL must include a database name")
		}
		query := connection.Query()
		if !query.Has("sslmode") {
			query.Set("sslmode", sslMode)
		}
		if !query.Has("search_path") {
			query.Set("search_path", schema)
		}
		connection.RawQuery = query.Encode()
		return connection.String(), nil
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
	return connection.String(), nil
}

func (s *Store) Initialize(ctx context.Context) error {
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

func (s *Store) Health(parent context.Context) (map[string]string, error) {
	ctx, cancel := context.WithTimeout(parent, time.Second)
	defer cancel()
	if err := s.db.PingContext(ctx); err != nil {
		return map[string]string{"status": "down", "error": err.Error()}, fmt.Errorf("check postgres health: %w", err)
	}
	stats := s.db.Stats()
	return map[string]string{
		"status": "up", "message": "database is healthy",
		"open_connections": strconv.Itoa(stats.OpenConnections),
		"in_use":           strconv.Itoa(stats.InUse), "idle": strconv.Itoa(stats.Idle),
	}, nil
}

func (s *Store) Menu(ctx context.Context, includeUnavailable bool) (models.Menu, error) {
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
			return models.Menu{}, errors.Join(
				fmt.Errorf("scan menu item: %w", err),
				wrapError("close menu item rows", rows.Close()),
			)
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
	if err := errors.Join(
		wrapError("iterate menu items", rows.Err()),
		wrapError("close menu item rows", rows.Close()),
	); err != nil {
		return models.Menu{}, err
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
			return models.Menu{}, errors.Join(
				fmt.Errorf("scan variant: %w", err),
				wrapError("close variant rows", variantRows.Close()),
			)
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
	if err := errors.Join(
		wrapError("iterate variants", variantRows.Err()),
		wrapError("close variant rows", variantRows.Close()),
	); err != nil {
		return models.Menu{}, err
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

func (s *Store) Categories(ctx context.Context) ([]models.MenuCategory, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, sort_order FROM category ORDER BY sort_order, name`)
	if err != nil {
		return nil, fmt.Errorf("query categories: %w", err)
	}
	result := []models.MenuCategory{}
	for rows.Next() {
		var category models.MenuCategory
		if err := rows.Scan(&category.ID, &category.Name, &category.SortOrder); err != nil {
			return nil, errors.Join(
				fmt.Errorf("scan category: %w", err),
				wrapError("close category rows", rows.Close()),
			)
		}
		result = append(result, category)
	}
	if err := errors.Join(
		wrapError("iterate categories", rows.Err()),
		wrapError("close category rows", rows.Close()),
	); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *Store) CreateCategories(ctx context.Context, categories []models.NewCategory) ([]models.MenuCategory, error) {
	return inTransaction(ctx, s.db, "create categories", func(tx *sql.Tx) ([]models.MenuCategory, error) {
		created := make([]models.MenuCategory, 0, len(categories))
		for _, category := range categories {
			var value models.MenuCategory
			err := tx.QueryRowContext(ctx, `
				INSERT INTO category (name, sort_order) VALUES ($1, $2)
				RETURNING id, name, sort_order`, category.Name, category.SortOrder,
			).Scan(&value.ID, &value.Name, &value.SortOrder)
			if err != nil {
				return nil, mapWriteError(err)
			}
			created = append(created, value)
		}
		return created, nil
	})
}

func (s *Store) UpdateInventory(ctx context.Context, itemID int64, quantity int) (models.Inventory, error) {
	var inventory models.Inventory
	err := s.db.QueryRowContext(ctx, `
		UPDATE menu_item SET stock_qty = $2, updated_at = NOW()
		WHERE id = $1 AND track_stock
		RETURNING id, stock_qty, available, updated_at`, itemID, quantity,
	).Scan(&inventory.ItemID, &inventory.Quantity, &inventory.Available, &inventory.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
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

func (s *Store) UpdateVariantInventory(ctx context.Context, variantOptionID int64, quantity int) (models.VariantInventory, error) {
	var inventory models.VariantInventory
	err := s.db.QueryRowContext(ctx, `
		UPDATE variant_option SET stock_qty = $2
		WHERE id = $1 AND track_stock
		RETURNING id, stock_qty`, variantOptionID, quantity,
	).Scan(&inventory.VariantOptionID, &inventory.Quantity)
	if errors.Is(err, sql.ErrNoRows) {
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

func (s *Store) UpdateItemAvailability(ctx context.Context, itemID int64, available bool) (models.ItemAvailability, error) {
	var availability models.ItemAvailability
	err := s.db.QueryRowContext(ctx, `
		UPDATE menu_item SET available = $2, updated_at = NOW()
		WHERE id = $1 RETURNING id, available, updated_at`, itemID, available,
	).Scan(&availability.ItemID, &availability.Available, &availability.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return models.ItemAvailability{}, ErrNotFound
	}
	if err != nil {
		return models.ItemAvailability{}, fmt.Errorf("update item availability: %w", err)
	}
	return availability, nil
}

func (s *Store) UpdateItemImage(ctx context.Context, itemID int64, imageURL string) (models.ItemImage, error) {
	var image models.ItemImage
	err := s.db.QueryRowContext(ctx, `
		UPDATE menu_item SET image_url = NULLIF($2, ''), updated_at = NOW()
		WHERE id = $1
		RETURNING id, COALESCE(image_url, ''), updated_at`, itemID, imageURL,
	).Scan(&image.ItemID, &image.ImageURL, &image.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return models.ItemImage{}, ErrNotFound
	}
	if err != nil {
		return models.ItemImage{}, fmt.Errorf("update item image: %w", err)
	}
	return image, nil
}

func (s *Store) CreateMenuItems(ctx context.Context, newItems []models.NewItem) ([]models.Item, error) {
	return inTransaction(ctx, s.db, "create menu items", func(tx *sql.Tx) ([]models.Item, error) {
		created := make([]models.Item, 0, len(newItems))
		for _, newItem := range newItems {
			item, err := insertMenuItem(ctx, tx, newItem)
			if err != nil {
				return nil, err
			}
			created = append(created, item)
		}
		return created, nil
	})
}

func insertMenuItem(ctx context.Context, tx *sql.Tx, newItem models.NewItem) (models.Item, error) {
	available := newItem.Available == nil || *newItem.Available
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
	).Scan(
		&item.ID, &item.CategoryID, &item.Name, &item.Description, &item.ImageURL,
		&item.PriceCents, &item.Available, &item.TrackStock, &stock,
	)
	if err != nil {
		return models.Item{}, mapWriteError(err)
	}
	setItemStock(&item, stock)
	item.VariantGroups = []models.VariantGroup{}

	if newItem.VariantGroup == nil {
		return item, nil
	}
	group, err := insertVariantGroup(ctx, tx, item.ID, *newItem.VariantGroup)
	if err != nil {
		return models.Item{}, err
	}
	item.VariantGroups = append(item.VariantGroups, group)
	return item, nil
}

func insertVariantGroup(ctx context.Context, tx *sql.Tx, itemID int64, newGroup models.NewVariantGroup) (models.VariantGroup, error) {
	group := models.VariantGroup{Name: newGroup.Name, Required: true, Options: []models.VariantOption{}}
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO variant_group (menu_item_id, name, required)
		VALUES ($1, $2, TRUE) RETURNING id`, itemID, group.Name,
	).Scan(&group.ID); err != nil {
		return models.VariantGroup{}, mapWriteError(err)
	}

	for optionIndex, newOption := range newGroup.Options {
		option, err := insertVariantOption(ctx, tx, group.ID, optionIndex, newOption)
		if err != nil {
			return models.VariantGroup{}, err
		}
		group.Options = append(group.Options, option)
	}
	return group, nil
}

func insertVariantOption(
	ctx context.Context,
	tx *sql.Tx,
	groupID int64,
	sortOrder int,
	newOption models.NewVariantOption,
) (models.VariantOption, error) {
	var option models.VariantOption
	var stock sql.NullInt64
	err := tx.QueryRowContext(ctx, `
		INSERT INTO variant_option
		    (variant_group_id, name, price_cents, track_stock, stock_qty, sort_order)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, name, price_cents, track_stock, stock_qty`,
		groupID, newOption.Name, newOption.PriceCents, newOption.TrackStock,
		newOption.StockQuantity, sortOrder,
	).Scan(&option.ID, &option.Name, &option.PriceCents, &option.TrackStock, &stock)
	if err != nil {
		return models.VariantOption{}, mapWriteError(err)
	}
	option.Available = true
	if stock.Valid {
		quantity := int(stock.Int64)
		option.StockQuantity = &quantity
		option.Available = quantity > 0
	}
	return option, nil
}

func setItemStock(item *models.Item, stock sql.NullInt64) {
	if !stock.Valid {
		return
	}
	quantity := int(stock.Int64)
	item.StockQuantity = &quantity
	item.Available = item.Available && quantity > 0
}

func mapWriteError(err error) error {
	var pgError *pgconn.PgError
	if errors.As(err, &pgError) {
		switch pgError.Code {
		case "23505":
			return fmt.Errorf("%w: %w", ErrConflict, err)
		case "23503":
			return fmt.Errorf("%w: %w", ErrInvalidCategory, err)
		}
	}
	return fmt.Errorf("write database record: %w", err)
}

func inTransaction[T any](
	ctx context.Context,
	db *sql.DB,
	operation string,
	fn func(*sql.Tx) (T, error),
) (T, error) {
	var zero T
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return zero, fmt.Errorf("begin %s transaction: %w", operation, err)
	}
	finished := false
	defer func() {
		if !finished {
			_ = tx.Rollback()
		}
	}()

	result, operationErr := fn(tx)
	if operationErr != nil {
		rollbackErr := rollbackError(tx, operation)
		finished = true
		return zero, errors.Join(operationErr, rollbackErr)
	}
	if err := tx.Commit(); err != nil {
		rollbackErr := rollbackError(tx, operation)
		finished = true
		return zero, errors.Join(
			fmt.Errorf("commit %s transaction: %w", operation, err),
			rollbackErr,
		)
	}
	finished = true
	return result, nil
}

func rollbackError(tx *sql.Tx, operation string) error {
	err := tx.Rollback()
	if err == nil || errors.Is(err, sql.ErrTxDone) {
		return nil
	}
	return fmt.Errorf("roll back %s transaction: %w", operation, err)
}

func wrapError(operation string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", operation, err)
}

func (s *Store) CreateOrder(ctx context.Context, request models.SubmitOrderRequest, quote models.Quote) (models.Order, error) {
	orderCode, err := randomHex(6)
	if err != nil {
		return models.Order{}, err
	}
	order := models.Order{
		OrderNumber: "VL-" + strings.ToUpper(orderCode), TableNumber: request.TableNumber,
		GuestCount: request.GuestCount, Status: models.OrderStatusNew,
		TotalCents: quote.TotalCents, Items: quote.Items,
	}
	return inTransaction(ctx, s.db, "create order", func(tx *sql.Tx) (models.Order, error) {
		if err := tx.QueryRowContext(ctx, `
			INSERT INTO orders (order_number, table_number, guest_count, status)
			VALUES ($1, $2, $3, 'new') RETURNING id, created_at`,
			order.OrderNumber, order.TableNumber, order.GuestCount,
		).Scan(&order.ID, &order.CreatedAt); err != nil {
			return models.Order{}, fmt.Errorf("insert order: %w", err)
		}
		for _, item := range quote.Items {
			if err := insertOrderItem(ctx, tx, order.ID, item); err != nil {
				return models.Order{}, err
			}
		}
		return order, nil
	})
}

func insertOrderItem(ctx context.Context, tx *sql.Tx, orderID int64, item models.QuoteLineItem) error {
	var variantID any
	if item.Variant != nil {
		variantID = item.Variant.ID
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO order_item
		    (order_id, menu_item_id, variant_option_id, quantity, unit_price_cents)
		VALUES ($1, $2, $3, $4, $5)`,
		orderID, item.ItemID, variantID, item.Quantity, item.UnitPriceCents,
	); err != nil {
		return fmt.Errorf("insert order item: %w", err)
	}
	return nil
}

func (s *Store) Orders(ctx context.Context, status string) ([]models.Order, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT o.id, o.order_number, o.staff_id, COALESCE(s.name, ''), o.table_number,
		       o.guest_count, o.status, o.created_at, o.sold_at
		FROM orders o LEFT JOIN staff s ON s.id = o.staff_id
		WHERE ($1 = '' OR o.status = $1)
		ORDER BY o.created_at DESC LIMIT 200`, status)
	if err != nil {
		return nil, fmt.Errorf("query orders: %w", err)
	}
	result := make([]models.Order, 0)
	for rows.Next() {
		order, err := scanOrder(rows)
		if err != nil {
			return nil, errors.Join(err, wrapError("close order rows", rows.Close()))
		}
		result = append(result, order)
	}
	iterationErr := rows.Err()
	closeErr := rows.Close()
	if err := errors.Join(wrapError("iterate orders", iterationErr), wrapError("close order rows", closeErr)); err != nil {
		return nil, err
	}

	for index := range result {
		if err := s.loadOrderItems(ctx, &result[index]); err != nil {
			return nil, err
		}
	}
	return result, nil
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

func (s *Store) UpdateOrderStatus(ctx context.Context, orderID int64, status string, staffID int64) (models.Order, error) {
	_, err := inTransaction(ctx, s.db, "update order status", func(tx *sql.Tx) (struct{}, error) {
		if err := requireActiveStaff(ctx, tx, staffID); err != nil {
			return struct{}{}, err
		}
		currentStatus, err := lockedOrderStatus(ctx, tx, orderID)
		if err != nil {
			return struct{}{}, err
		}
		if currentStatus == status {
			return struct{}{}, nil
		}
		if currentStatus != models.OrderStatusNew ||
			(status != models.OrderStatusSold && status != models.OrderStatusCancelled) {
			return struct{}{}, ErrInvalidTransition
		}

		if status == models.OrderStatusSold {
			if err := decrementOrderStock(ctx, tx, orderID); err != nil {
				return struct{}{}, err
			}
			if _, err := tx.ExecContext(ctx, `
				UPDATE orders SET status = 'sold', staff_id = $2, sold_at = NOW()
				WHERE id = $1`, orderID, staffID); err != nil {
				return struct{}{}, fmt.Errorf("mark order sold: %w", err)
			}
			return struct{}{}, nil
		}

		if _, err := tx.ExecContext(ctx, `
			UPDATE orders SET status = 'cancelled', staff_id = $2 WHERE id = $1`, orderID, staffID); err != nil {
			return struct{}{}, fmt.Errorf("cancel order: %w", err)
		}
		return struct{}{}, nil
	})
	if err != nil {
		return models.Order{}, err
	}
	return s.orderByID(ctx, orderID)
}

func requireActiveStaff(ctx context.Context, tx *sql.Tx, staffID int64) error {
	var active bool
	err := tx.QueryRowContext(ctx, `SELECT active FROM staff WHERE id = $1`, staffID).Scan(&active)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !active) {
		return ErrInactiveStaff
	}
	if err != nil {
		return fmt.Errorf("query staff status: %w", err)
	}
	return nil
}

func lockedOrderStatus(ctx context.Context, tx *sql.Tx, orderID int64) (string, error) {
	var status string
	err := tx.QueryRowContext(ctx, `SELECT status FROM orders WHERE id = $1 FOR UPDATE`, orderID).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("lock order: %w", err)
	}
	return status, nil
}

func decrementOrderStock(ctx context.Context, tx *sql.Tx, orderID int64) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT oi.menu_item_id, oi.variant_option_id, oi.quantity,
		       mi.track_stock, COALESCE(vo.track_stock, FALSE)
		FROM order_item oi
		JOIN menu_item mi ON mi.id = oi.menu_item_id
		LEFT JOIN variant_option vo ON vo.id = oi.variant_option_id
		WHERE oi.order_id = $1`, orderID)
	if err != nil {
		return fmt.Errorf("query order stock: %w", err)
	}

	itemQuantities := make(map[int64]int)
	variantQuantities := make(map[int64]int)
	for rows.Next() {
		var itemID int64
		var variantID sql.NullInt64
		var quantity int
		var itemTracks, variantTracks bool
		if err := rows.Scan(&itemID, &variantID, &quantity, &itemTracks, &variantTracks); err != nil {
			return errors.Join(
				fmt.Errorf("scan order stock: %w", err),
				wrapError("close order stock rows", rows.Close()),
			)
		}
		switch {
		case variantID.Valid && variantTracks:
			variantQuantities[variantID.Int64] += quantity
		case itemTracks:
			itemQuantities[itemID] += quantity
		}
	}
	iterationErr := rows.Err()
	closeErr := rows.Close()
	if err := errors.Join(wrapError("iterate order stock", iterationErr), wrapError("close order stock rows", closeErr)); err != nil {
		return err
	}

	// Deterministic lock ordering prevents two multi-item sales from deadlocking
	// when they contain the same inventory in a different line order.
	for _, itemID := range slices.Sorted(maps.Keys(itemQuantities)) {
		if err := decrementItemStock(ctx, tx, itemID, itemQuantities[itemID]); err != nil {
			return err
		}
	}
	for _, variantID := range slices.Sorted(maps.Keys(variantQuantities)) {
		if err := decrementVariantStock(ctx, tx, variantID, variantQuantities[variantID]); err != nil {
			return err
		}
	}
	return nil
}

func decrementItemStock(ctx context.Context, tx *sql.Tx, itemID int64, quantity int) error {
	result, err := tx.ExecContext(ctx, `
		UPDATE menu_item SET stock_qty = stock_qty - $2, updated_at = NOW()
		WHERE id = $1 AND track_stock AND stock_qty >= $2`, itemID, quantity)
	if err != nil {
		return fmt.Errorf("decrement item stock: %w", err)
	}
	return requireOneAffectedRow(result)
}

func decrementVariantStock(ctx context.Context, tx *sql.Tx, variantID int64, quantity int) error {
	result, err := tx.ExecContext(ctx, `
		UPDATE variant_option SET stock_qty = stock_qty - $2
		WHERE id = $1 AND track_stock AND stock_qty >= $2`, variantID, quantity)
	if err != nil {
		return fmt.Errorf("decrement variant stock: %w", err)
	}
	return requireOneAffectedRow(result)
}

func requireOneAffectedRow(result sql.Result) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read affected inventory rows: %w", err)
	}
	if affected != 1 {
		return ErrInsufficientStock
	}
	return nil
}

func (s *Store) orderByID(ctx context.Context, orderID int64) (models.Order, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT o.id, o.order_number, o.staff_id, COALESCE(s.name, ''), o.table_number,
		       o.guest_count, o.status, o.created_at, o.sold_at
		FROM orders o LEFT JOIN staff s ON s.id = o.staff_id WHERE o.id = $1`, orderID)
	order, err := scanOrder(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return models.Order{}, ErrNotFound
		}
		return models.Order{}, err
	}
	if err := s.loadOrderItems(ctx, &order); err != nil {
		return models.Order{}, err
	}
	return order, nil
}

func (s *Store) loadOrderItems(ctx context.Context, order *models.Order) error {
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
	order.Items = []models.QuoteLineItem{}
	order.TotalCents = 0
	for rows.Next() {
		var item models.QuoteLineItem
		var variantID sql.NullInt64
		var variantName string
		if err := rows.Scan(&item.ItemID, &item.Name, &item.Quantity, &item.UnitPriceCents, &variantID, &variantName); err != nil {
			return errors.Join(
				fmt.Errorf("scan order item: %w", err),
				wrapError("close order item rows", rows.Close()),
			)
		}
		if variantID.Valid {
			item.Variant = &models.SelectedVariantOption{ID: variantID.Int64, Name: variantName}
		}
		item.LineTotalCents = item.UnitPriceCents * item.Quantity
		order.TotalCents += item.LineTotalCents
		order.Items = append(order.Items, item)
	}
	if err := errors.Join(
		wrapError("iterate order items", rows.Err()),
		wrapError("close order item rows", rows.Close()),
	); err != nil {
		return err
	}
	return nil
}

func (s *Store) Staff(ctx context.Context, includeInactive bool) ([]models.Staff, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, active, created_at FROM staff
		WHERE $1 OR active ORDER BY name`, includeInactive)
	if err != nil {
		return nil, fmt.Errorf("query staff: %w", err)
	}
	result := []models.Staff{}
	for rows.Next() {
		var member models.Staff
		if err := rows.Scan(&member.ID, &member.Name, &member.Active, &member.CreatedAt); err != nil {
			return nil, errors.Join(
				fmt.Errorf("scan staff: %w", err),
				wrapError("close staff rows", rows.Close()),
			)
		}
		result = append(result, member)
	}
	if err := errors.Join(
		wrapError("iterate staff", rows.Err()),
		wrapError("close staff rows", rows.Close()),
	); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *Store) StaffActive(ctx context.Context, staffID int64) (bool, error) {
	var active bool
	err := s.db.QueryRowContext(ctx, `SELECT active FROM staff WHERE id = $1`, staffID).Scan(&active)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrNotFound
	}
	if err != nil {
		return false, fmt.Errorf("query staff status: %w", err)
	}
	return active, nil
}

func (s *Store) StaffCredentials(ctx context.Context, name string) (models.Staff, string, error) {
	var member models.Staff
	var pinHash string
	err := s.db.QueryRowContext(ctx, `
		SELECT id, name, pin_hash, active, created_at
		FROM staff WHERE LOWER(name) = LOWER($1)`, name,
	).Scan(&member.ID, &member.Name, &pinHash, &member.Active, &member.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return models.Staff{}, "", ErrNotFound
	}
	if err != nil {
		return models.Staff{}, "", fmt.Errorf("query staff credentials: %w", err)
	}
	return member, pinHash, nil
}

func (s *Store) CreateStaff(ctx context.Context, name, pinHash string) (models.Staff, error) {
	var member models.Staff
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO staff (name, pin_hash) VALUES ($1, $2)
		RETURNING id, name, active, created_at`, name, pinHash,
	).Scan(&member.ID, &member.Name, &member.Active, &member.CreatedAt)
	if err != nil {
		return models.Staff{}, mapWriteError(err)
	}
	return member, nil
}

func (s *Store) SetStaffActive(ctx context.Context, staffID int64, active bool) (models.Staff, error) {
	var member models.Staff
	err := s.db.QueryRowContext(ctx, `
		UPDATE staff SET active = $2 WHERE id = $1
		RETURNING id, name, active, created_at`, staffID, active,
	).Scan(&member.ID, &member.Name, &member.Active, &member.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return models.Staff{}, ErrNotFound
	}
	if err != nil {
		return models.Staff{}, fmt.Errorf("update staff: %w", err)
	}
	return member, nil
}

func (s *Store) MenuQR(ctx context.Context) (models.Link, error) {
	var link models.Link
	if err := s.db.QueryRowContext(ctx, `
		SELECT dub_link_id, short_link, qr_code, destination, created_at
		FROM menu_qr_link WHERE singleton = TRUE`).Scan(
		&link.ID, &link.ShortLink, &link.QRCode, &link.Destination, &link.CreatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return models.Link{}, ErrNotFound
		}
		return models.Link{}, fmt.Errorf("query menu QR link: %w", err)
	}
	return link, nil
}

func (s *Store) SaveMenuQR(ctx context.Context, link models.Link) error {
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
	if byteCount < 1 {
		return "", errors.New("identifier byte count must be positive")
	}
	value := make([]byte, byteCount)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate identifier: %w", err)
	}
	return hex.EncodeToString(value), nil
}

func (s *Store) Close() error { return s.db.Close() }

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
