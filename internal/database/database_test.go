package database

import (
	"context"
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
	var (
		dbName = "database"
		dbPwd  = "password"
		dbUser = "user"
	)

	dbContainer, err := postgres.Run(
		context.Background(),
		"postgres:latest",
		postgres.WithDatabase(dbName),
		postgres.WithUsername(dbUser),
		postgres.WithPassword(dbPwd),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(5*time.Second)),
	)
	if err != nil {
		return nil, err
	}

	database = dbName
	password = dbPwd
	username = dbUser

	dbHost, err := dbContainer.Host(context.Background())
	if err != nil {
		return dbContainer.Terminate, err
	}

	dbPort, err := dbContainer.MappedPort(context.Background(), "5432/tcp")
	if err != nil {
		return dbContainer.Terminate, err
	}

	host = dbHost
	port = dbPort.Port()

	return dbContainer.Terminate, err
}

func TestMain(m *testing.M) {
	teardown, err := mustStartPostgresContainer()
	if err != nil {
		postgresAvailable = false
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

func TestNew(t *testing.T) {
	srv := New()
	if srv == nil {
		t.Fatal("New() returned nil")
	}
}

func TestHealth(t *testing.T) {
	requirePostgres(t)
	srv := New()
	t.Cleanup(func() { _ = srv.Close() })

	stats := srv.Health(context.Background())

	if stats["status"] != "up" {
		t.Fatalf("expected status to be up, got %s", stats["status"])
	}

	if _, ok := stats["error"]; ok {
		t.Fatalf("expected error not to be present")
	}

	if stats["message"] != "database is healthy" {
		t.Fatalf("unexpected health message: %s", stats["message"])
	}
}

func TestInitializeAndMenu(t *testing.T) {
	requirePostgres(t)
	srv := New()
	t.Cleanup(func() { _ = srv.Close() })
	if err := srv.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() returned an error: %v", err)
	}

	current, err := srv.Menu(context.Background(), false)
	if err != nil {
		t.Fatalf("Menu() returned an error: %v", err)
	}
	if len(current.Categories) == 0 {
		t.Fatal("expected seeded menu categories")
	}
}

func TestUpdateInventory(t *testing.T) {
	requirePostgres(t)
	srv := New()
	t.Cleanup(func() { _ = srv.Close() })
	if err := srv.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() returned an error: %v", err)
	}

	inventory, err := srv.UpdateInventory(context.Background(), "butter-croissant", 0)
	if err != nil {
		t.Fatalf("UpdateInventory() returned an error: %v", err)
	}
	if inventory.Available || inventory.Quantity != 0 {
		t.Fatalf("expected sold-out inventory, got %+v", inventory)
	}
}

func TestUpdateItemImage(t *testing.T) {
	requirePostgres(t)
	srv := New()
	t.Cleanup(func() { _ = srv.Close() })
	if err := srv.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() returned an error: %v", err)
	}

	imageURL := "/api/v1/images/menu/test-carrot-cake.jpg"
	updated, err := srv.UpdateItemImage(context.Background(), "butter-croissant", imageURL)
	if err != nil {
		t.Fatalf("UpdateItemImage() returned an error: %v", err)
	}
	if updated.ItemID != "butter-croissant" || updated.ImageURL != imageURL || updated.UpdatedAt.IsZero() {
		t.Fatalf("unexpected image update: %+v", updated)
	}

	current, err := srv.Menu(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, category := range current.Categories {
		for _, item := range category.Items {
			if item.ID == "butter-croissant" {
				found = true
				if item.ImageURL != imageURL {
					t.Fatalf("updated image URL was not returned by the menu: %+v", item)
				}
			}
		}
	}
	if !found {
		t.Fatal("updated menu item was not returned by the menu")
	}
}

func TestCreateMenuItemsInOneTransaction(t *testing.T) {
	requirePostgres(t)
	srv := New()
	t.Cleanup(func() { _ = srv.Close() })
	if err := srv.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() returned an error: %v", err)
	}

	items, err := srv.CreateMenuItems(context.Background(), []models.NewItem{
		{
			ID: "test-carrot-cake", CategoryID: "cakes", Name: "Test Carrot Cake",
			Type: "cake", PriceCents: 750, Currency: "USD", Quantity: 4,
		},
		{
			ID: "test-flat-white", CategoryID: "drinks", Name: "Test Flat White",
			Type: "drink", PriceCents: 525, Currency: "USD", Quantity: 20,
			ModifierGroups: []models.NewModifierGroup{{
				ID: "test-flat-white-milk", Name: "Milk", MinSelections: 1, MaxSelections: 1,
				Options: []models.NewModifierOption{{
					ID: "test-flat-white-whole", Name: "Whole milk",
				}},
			}},
		},
	})
	if err != nil {
		t.Fatalf("CreateMenuItems() returned an error: %v", err)
	}
	if len(items) != 2 || len(items[1].ModifierGroups) != 1 {
		t.Fatalf("unexpected created items: %+v", items)
	}
	if items[0].ImageURL != "" {
		t.Fatalf("expected an item created without an image to have an empty image URL, got %q", items[0].ImageURL)
	}

	current, err := srv.Menu(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if itemQuantity(t, current, "test-carrot-cake") != 4 || itemQuantity(t, current, "test-flat-white") != 20 {
		t.Fatal("bulk-created items were not returned by the menu")
	}
}

func TestCreateAndListCustomCategories(t *testing.T) {
	requirePostgres(t)
	srv := New()
	t.Cleanup(func() { _ = srv.Close() })
	if err := srv.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() returned an error: %v", err)
	}

	created, err := srv.CreateCategories(context.Background(), []models.NewCategory{{
		ID: "test-cold-beverages", Name: "Test Cold Beverages",
		Description: "Iced drinks", SortOrder: 50,
	}})
	if err != nil {
		t.Fatalf("CreateCategories() returned an error: %v", err)
	}
	if len(created) != 1 || created[0].ID != "test-cold-beverages" {
		t.Fatalf("unexpected created categories: %+v", created)
	}

	categories, err := srv.Categories(context.Background())
	if err != nil {
		t.Fatalf("Categories() returned an error: %v", err)
	}
	found := false
	for _, category := range categories {
		if category.ID == "test-cold-beverages" && category.Name == "Test Cold Beverages" {
			found = true
		}
	}
	if !found {
		t.Fatal("custom category was not returned for the dropdown")
	}
}

func TestOrderInventoryChangesOnlyWhenMarkedSold(t *testing.T) {
	requirePostgres(t)
	srv := New()
	t.Cleanup(func() { _ = srv.Close() })
	if err := srv.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize() returned an error: %v", err)
	}

	before, err := srv.Menu(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	beforeQuantity := itemQuantity(t, before, "chocolate-cake-slice")
	quote := models.Quote{
		Currency: "USD", SubtotalCents: 775,
		Items: []models.QuoteLineItem{{
			ItemID: "chocolate-cake-slice", Name: "Chocolate Cake Slice",
			Quantity: 1, UnitPriceCents: 775, LineTotalCents: 775,
		}},
	}
	order, err := srv.CreateOrder(context.Background(), models.SubmitOrderRequest{
		CustomerName: "Maya", Items: []models.QuoteItemRequest{{ItemID: "chocolate-cake-slice", Quantity: 1}},
	}, quote)
	if err != nil {
		t.Fatalf("CreateOrder() returned an error: %v", err)
	}
	if order.Status != "submitted" {
		t.Fatalf("expected submitted status, got %q", order.Status)
	}

	afterSubmit, err := srv.Menu(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if got := itemQuantity(t, afterSubmit, "chocolate-cake-slice"); got != beforeQuantity {
		t.Fatalf("submission changed stock: before=%d after=%d", beforeQuantity, got)
	}

	sold, err := srv.UpdateOrderStatus(context.Background(), order.ID, "sold")
	if err != nil {
		t.Fatalf("UpdateOrderStatus() returned an error: %v", err)
	}
	if sold.Status != "sold" || sold.SoldAt == nil {
		t.Fatalf("expected sold order, got %+v", sold)
	}
	afterSale, err := srv.Menu(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if got := itemQuantity(t, afterSale, "chocolate-cake-slice"); got != beforeQuantity-1 {
		t.Fatalf("sale did not decrement stock: before=%d after=%d", beforeQuantity, got)
	}
}

func itemQuantity(t *testing.T, current models.Menu, itemID string) int {
	t.Helper()
	for _, category := range current.Categories {
		for _, item := range category.Items {
			if item.ID == itemID {
				return item.QuantityAvailable
			}
		}
	}
	t.Fatalf("item %q was not found", itemID)
	return 0
}

func TestClose(t *testing.T) {
	srv := New()

	if srv.Close() != nil {
		t.Fatalf("expected Close() to return nil")
	}
}
