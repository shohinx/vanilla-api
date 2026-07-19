package database

import (
	"context"
	"errors"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/shohinx/vanilla-api/internal/sdk/models"
)

var postgresAvailable bool

func mustStartPostgresContainer() (func(context.Context, ...testcontainers.TerminateOption) error, error) {
	dbContainer, err := postgres.Run(
		context.Background(), "postgres:latest",
		postgres.WithDatabase("database"), postgres.WithUsername("user"), postgres.WithPassword("password"),
		testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").
			WithOccurrence(2).WithStartupTimeout(5*time.Second)),
	)
	if err != nil {
		return nil, err
	}
	_ = os.Unsetenv("DATABASE_URL")
	_ = os.Setenv("BLUEPRINT_DB_DATABASE", "database")
	_ = os.Setenv("BLUEPRINT_DB_PASSWORD", "password")
	_ = os.Setenv("BLUEPRINT_DB_USERNAME", "user")
	host, err := dbContainer.Host(context.Background())
	if err != nil {
		return dbContainer.Terminate, err
	}
	port, err := dbContainer.MappedPort(context.Background(), "5432/tcp")
	if err != nil {
		return dbContainer.Terminate, err
	}
	_ = os.Setenv("BLUEPRINT_DB_HOST", host)
	_ = os.Setenv("BLUEPRINT_DB_PORT", port.Port())
	return dbContainer.Terminate, nil
}

func TestMain(m *testing.M) {
	teardown, err := mustStartPostgresContainer()
	if err != nil {
		os.Exit(m.Run())
	}
	postgresAvailable = true
	code := m.Run()
	if teardown != nil && teardown(context.Background()) != nil {
		code = 1
	}
	os.Exit(code)
}

func requirePostgres(t *testing.T) {
	t.Helper()
	if !postgresAvailable {
		t.Skip("Docker is unavailable; skipping PostgreSQL integration test")
	}
}

func initializedService(t *testing.T) Service {
	t.Helper()
	requirePostgres(t)
	srv := New()
	t.Cleanup(func() { _ = srv.Close() })
	if err := srv.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() returned an error: %v", err)
	}
	return srv
}

func TestNew(t *testing.T) {
	if New() == nil {
		t.Fatal("New() returned nil")
	}
}

func TestDatabaseConnectionURLUsesURI(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgresql://uri-user:uri-password@db.example.com:6543/uri_db")
	t.Setenv("BLUEPRINT_DB_SSLMODE", "require")
	t.Setenv("BLUEPRINT_DB_SCHEMA", "bakery")
	connection, err := url.Parse(databaseConnectionURL())
	if err != nil {
		t.Fatalf("parse database connection URL: %v", err)
	}
	if connection.Scheme != "postgresql" || connection.Host != "db.example.com:6543" || connection.Path != "/uri_db" {
		t.Fatalf("unexpected database connection URL: %s", connection.String())
	}
	if connection.User.String() != "uri-user:uri-password" {
		t.Fatalf("unexpected database user info: %s", connection.User.String())
	}
	if connection.Query().Get("sslmode") != "require" || connection.Query().Get("search_path") != "bakery" {
		t.Fatalf("expected URI defaults to be applied, got query %q", connection.RawQuery)
	}
}

func TestDatabaseConnectionURLPreservesURIOptions(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://user:password@localhost:5432/database?sslmode=verify-full&search_path=custom")
	t.Setenv("BLUEPRINT_DB_SSLMODE", "disable")
	t.Setenv("BLUEPRINT_DB_SCHEMA", "public")
	connection, err := url.Parse(databaseConnectionURL())
	if err != nil {
		t.Fatalf("parse database connection URL: %v", err)
	}
	if connection.Query().Get("sslmode") != "verify-full" || connection.Query().Get("search_path") != "custom" {
		t.Fatalf("expected URI options to be preserved, got query %q", connection.RawQuery)
	}
}

func TestHealth(t *testing.T) {
	srv := initializedService(t)
	stats := srv.Health(context.Background())
	if stats["status"] != "up" || stats["message"] != "database is healthy" {
		t.Fatalf("unexpected health response: %+v", stats)
	}
}

func TestSeededMenuUsesSimpleVariantsAndOptionalStock(t *testing.T) {
	srv := initializedService(t)
	current, err := srv.Menu(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	latte := itemByName(t, current, "Latte")
	if latte.TrackStock || latte.StockQuantity != nil || len(latte.VariantGroups) != 1 || latte.VariantGroups[0].Name != "Size" {
		t.Fatalf("unexpected made-to-order latte: %+v", latte)
	}
	if len(latte.VariantGroups[0].Options) != 2 {
		t.Fatalf("expected only the size choices, got %+v", latte.VariantGroups[0].Options)
	}
	cookie := itemByName(t, current, "Brown Butter Cookie")
	if !cookie.TrackStock || cookie.StockQuantity == nil || len(cookie.VariantGroups) != 0 {
		t.Fatalf("unexpected standalone cookie: %+v", cookie)
	}
}

func TestInitializeRemovesDiscardedPrototypeTables(t *testing.T) {
	srv := initializedService(t).(*service)
	if _, err := srv.db.ExecContext(context.Background(), `CREATE TABLE categories (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatalf("create prototype table: %v", err)
	}
	if err := srv.Initialize(context.Background()); err != nil {
		t.Fatalf("reinitialize schema: %v", err)
	}
	var removed bool
	if err := srv.db.QueryRowContext(context.Background(), `SELECT to_regclass('categories') IS NULL`).Scan(&removed); err != nil {
		t.Fatalf("check prototype table: %v", err)
	}
	if !removed {
		t.Fatal("discarded prototype table still exists")
	}
}

func TestInventoryOnlyAppliesToTrackedItems(t *testing.T) {
	srv := initializedService(t)
	current, _ := srv.Menu(context.Background(), true)
	cake := itemByName(t, current, "Chocolate Cake Slice")
	inventory, err := srv.UpdateInventory(context.Background(), cake.ID, 0)
	if err != nil || inventory.Available || inventory.Quantity != 0 {
		t.Fatalf("unexpected inventory update: inventory=%+v err=%v", inventory, err)
	}
	latte := itemByName(t, current, "Latte")
	if _, err := srv.UpdateInventory(context.Background(), latte.ID, 10); !errors.Is(err, ErrStockNotTracked) {
		t.Fatalf("expected untracked stock error, got %v", err)
	}
}

func TestUpdateItemImage(t *testing.T) {
	srv := initializedService(t)
	current, _ := srv.Menu(context.Background(), true)
	item := itemByName(t, current, "Butter Croissant")
	imageURL := "/api/v1/images/menu/test-croissant.webp"
	updated, err := srv.UpdateItemImage(context.Background(), item.ID, imageURL)
	if err != nil || updated.ImageURL != imageURL || updated.UpdatedAt.IsZero() {
		t.Fatalf("unexpected image update: updated=%+v err=%v", updated, err)
	}
}

func TestCreateMenuItemsWithOneRequiredVariant(t *testing.T) {
	srv := initializedService(t)
	categories, err := srv.Categories(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var drinksID int64
	for _, category := range categories {
		if category.Name == "Coffee & Tea" {
			drinksID = category.ID
		}
	}
	items, err := srv.CreateMenuItems(context.Background(), []models.NewItem{{
		CategoryID: drinksID, Name: "Test Flat White", PriceCents: 525,
		VariantGroup: &models.NewVariantGroup{Name: "Size", Options: []models.NewVariantOption{
			{Name: "Small", PriceCents: 525}, {Name: "Large", PriceCents: 600},
		}},
	}})
	if err != nil {
		t.Fatalf("CreateMenuItems() returned an error: %v", err)
	}
	if len(items) != 1 || len(items[0].VariantGroups) != 1 || len(items[0].VariantGroups[0].Options) != 2 {
		t.Fatalf("unexpected created item: %+v", items)
	}
}

func TestCreateAndListCustomCategories(t *testing.T) {
	srv := initializedService(t)
	created, err := srv.CreateCategories(context.Background(), []models.NewCategory{{Name: "Cold Beverages", SortOrder: 50}})
	if err != nil {
		t.Fatalf("CreateCategories() returned an error: %v", err)
	}
	if len(created) != 1 || created[0].ID < 1 {
		t.Fatalf("unexpected created categories: %+v", created)
	}
}

func TestOrderCapturesTableGuestsAndDecrementsOnlyWhenSold(t *testing.T) {
	srv := initializedService(t)
	staff, err := srv.CreateStaff(context.Background(), "Test Worker", "hashed-pin")
	if err != nil {
		t.Fatal(err)
	}
	current, _ := srv.Menu(context.Background(), true)
	cake := itemByName(t, current, "Lemon Olive Oil Cake")
	before := *cake.StockQuantity
	quote := models.Quote{TotalCents: cake.PriceCents, Items: []models.QuoteLineItem{{
		ItemID: cake.ID, Name: cake.Name, Quantity: 1,
		UnitPriceCents: cake.PriceCents, LineTotalCents: cake.PriceCents,
	}}}
	order, err := srv.CreateOrder(context.Background(), models.SubmitOrderRequest{
		TableNumber: "Patio 4", GuestCount: 3,
		Items: []models.QuoteItemRequest{{ItemID: cake.ID, Quantity: 1}},
	}, quote)
	if err != nil {
		t.Fatal(err)
	}
	if order.Status != "new" || order.TableNumber != "Patio 4" || order.GuestCount != 3 {
		t.Fatalf("unexpected new order: %+v", order)
	}
	afterSubmit, _ := srv.Menu(context.Background(), true)
	if got := *itemByName(t, afterSubmit, cake.Name).StockQuantity; got != before {
		t.Fatalf("submission changed stock: before=%d after=%d", before, got)
	}
	sold, err := srv.UpdateOrderStatus(context.Background(), order.ID, "sold", staff.ID)
	if err != nil {
		t.Fatal(err)
	}
	if sold.Status != "sold" || sold.SoldAt == nil || sold.StaffID == nil || *sold.StaffID != staff.ID {
		t.Fatalf("unexpected sold order: %+v", sold)
	}
	afterSale, _ := srv.Menu(context.Background(), true)
	if got := *itemByName(t, afterSale, cake.Name).StockQuantity; got != before-1 {
		t.Fatalf("sale did not decrement stock: before=%d after=%d", before, got)
	}
}

func itemByName(t *testing.T, current models.Menu, name string) models.Item {
	t.Helper()
	for _, category := range current.Categories {
		for _, item := range category.Items {
			if item.Name == name {
				return item
			}
		}
	}
	t.Fatalf("item %q was not found", name)
	return models.Item{}
}

func TestClose(t *testing.T) {
	srv := New()
	if srv.Close() != nil {
		t.Fatal("expected Close() to return nil")
	}
}
