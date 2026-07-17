package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/shohinx/vanilla-api/internal/database"
	"github.com/shohinx/vanilla-api/internal/dub"
	"github.com/shohinx/vanilla-api/internal/menu"
)

type fakeDatabase struct {
	menu               menu.Menu
	includeUnavailable bool
	inventory          menu.Inventory
	updateItemID       string
	updateQuantity     int
	createdOrder       menu.Order
	orders             []menu.Order
	updatedOrder       menu.Order
	updatedOrderID     string
	updatedStatus      string
	qrLink             dub.Link
	createdRequest     menu.SubmitOrderRequest
	createdMenuItems   []menu.NewItem
	categories         []menu.MenuCategory
	createdCategories  []menu.NewCategory
}

func (f *fakeDatabase) Initialize(context.Context) error { return nil }
func (f *fakeDatabase) Close() error                     { return nil }
func (f *fakeDatabase) Health(context.Context) map[string]string {
	return map[string]string{"status": "up"}
}
func (f *fakeDatabase) Menu(_ context.Context, includeUnavailable bool) (menu.Menu, error) {
	f.includeUnavailable = includeUnavailable
	return f.menu, nil
}

func (f *fakeDatabase) Categories(context.Context) ([]menu.MenuCategory, error) {
	return f.categories, nil
}

func (f *fakeDatabase) CreateCategories(_ context.Context, categories []menu.NewCategory) ([]menu.MenuCategory, error) {
	f.createdCategories = categories
	created := make([]menu.MenuCategory, 0, len(categories))
	for _, category := range categories {
		created = append(created, menu.MenuCategory{
			ID: category.ID, Name: category.Name,
			Description: category.Description, SortOrder: category.SortOrder,
		})
	}
	return created, nil
}
func (f *fakeDatabase) UpdateInventory(_ context.Context, itemID string, quantity int) (menu.Inventory, error) {
	f.updateItemID = itemID
	f.updateQuantity = quantity
	return f.inventory, nil
}

func (f *fakeDatabase) CreateMenuItems(_ context.Context, items []menu.NewItem) ([]menu.Item, error) {
	f.createdMenuItems = items
	created := make([]menu.Item, 0, len(items))
	for _, item := range items {
		created = append(created, menu.Item{ID: item.ID, Name: item.Name, Currency: item.Currency})
	}
	return created, nil
}

func (f *fakeDatabase) CreateOrder(_ context.Context, request menu.SubmitOrderRequest, quote menu.Quote) (menu.Order, error) {
	f.createdRequest = request
	f.createdOrder.Items = quote.Items
	f.createdOrder.SubtotalCents = quote.SubtotalCents
	f.createdOrder.Currency = quote.Currency
	return f.createdOrder, nil
}

func (f *fakeDatabase) Orders(context.Context, string) ([]menu.Order, error) {
	return f.orders, nil
}

func (f *fakeDatabase) UpdateOrderStatus(_ context.Context, orderID, status string) (menu.Order, error) {
	f.updatedOrderID = orderID
	f.updatedStatus = status
	return f.updatedOrder, nil
}

func (f *fakeDatabase) MenuQR(context.Context) (dub.Link, error) {
	if f.qrLink.ID == "" {
		return dub.Link{}, database.ErrNotFound
	}
	return f.qrLink, nil
}

func (f *fakeDatabase) SaveMenuQR(_ context.Context, link dub.Link) error {
	f.qrLink = link
	return nil
}

type fakeDub struct {
	link        dub.Link
	image       []byte
	retrieveErr error
}

func (f *fakeDub) CreateMenuLink(context.Context, string, string, string) (dub.Link, error) {
	return f.link, nil
}

func (f *fakeDub) RetrieveMenuLink(context.Context, string) (dub.Link, error) {
	if f.retrieveErr != nil {
		return dub.Link{}, f.retrieveErr
	}
	if f.link.ID == "" {
		return dub.Link{}, dub.ErrNotFound
	}
	return f.link, nil
}

func (f *fakeDub) QRCode(context.Context, string) ([]byte, error) {
	return f.image, nil
}

func testMenu() menu.Menu {
	return menu.Menu{
		GeneratedAt: time.Date(2026, time.July, 16, 12, 0, 0, 0, time.UTC),
		Categories: []menu.Category{{
			ID: "drinks", Name: "Drinks", Items: []menu.Item{{
				ID: "latte", Name: "Latte", Type: "drink", PriceCents: 550,
				Currency: "USD", Available: true, QuantityAvailable: 4,
				ModifierGroups: []menu.ModifierGroup{{
					ID: "latte-size", Name: "Size", MinSelections: 1, MaxSelections: 1,
					Options: []menu.ModifierOption{
						{ID: "latte-8oz", Name: "8 oz", Available: true},
						{ID: "latte-12oz", Name: "12 oz", PriceDeltaCents: 75, Available: true},
					},
				}},
			}},
		}},
	}
}

func newTestRouter(db *fakeDatabase) http.Handler {
	return New(db, &fakeDub{}, Config{
		AdminAPIKey:    "test-secret",
		MenuAppURL:     "https://menu.example.com",
		AllowedOrigins: []string{"http://localhost:5173"},
	}).RegisterRoutes()
}

func newTestRouterWithDub(db *fakeDatabase, dubService *fakeDub) http.Handler {
	return New(db, dubService, Config{
		AdminAPIKey:    "test-secret",
		MenuAppURL:     "https://menu.example.com",
		AllowedOrigins: []string{"http://localhost:5173"},
		DubAPIKey:      "dub_test",
		DubLinkKey:     "menu",
	}).RegisterRoutes()
}

func TestMenuHandler(t *testing.T) {
	db := &fakeDatabase{menu: testMenu()}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/menu", nil)
	response := httptest.NewRecorder()

	newTestRouter(db).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", response.Code, response.Body.String())
	}
	if db.includeUnavailable {
		t.Fatal("public menu should not request unavailable items")
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("expected live menu response to disable caching")
	}
}

func TestQuoteHandlerPricesCustomizations(t *testing.T) {
	db := &fakeDatabase{menu: testMenu()}
	body := bytes.NewBufferString(`{"items":[{"item_id":"latte","quantity":2,"option_ids":["latte-12oz"]}]}`)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/order-plans/quote", body)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	newTestRouter(db).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", response.Code, response.Body.String())
	}
	if !db.includeUnavailable {
		t.Fatal("quote should load unavailable items so it can validate stock")
	}
	var quote menu.Quote
	if err := json.Unmarshal(response.Body.Bytes(), &quote); err != nil {
		t.Fatal(err)
	}
	if quote.SubtotalCents != 1250 {
		t.Fatalf("expected subtotal 1250, got %d", quote.SubtotalCents)
	}
}

func TestQuoteHandlerRejectsMissingRequiredCustomization(t *testing.T) {
	db := &fakeDatabase{menu: testMenu()}
	body := bytes.NewBufferString(`{"items":[{"item_id":"latte","quantity":1}]}`)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/order-plans/quote", body)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	newTestRouter(db).ServeHTTP(response, request)

	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected status 422, got %d: %s", response.Code, response.Body.String())
	}
}

func TestSubmitOrderStoresOrderWithoutUpdatingInventory(t *testing.T) {
	db := &fakeDatabase{
		menu:         testMenu(),
		createdOrder: menu.Order{ID: "order-1", OrderNumber: "VL-123456", Status: "submitted"},
	}
	body := bytes.NewBufferString(`{
		"customer_name":"Maya",
		"notes":"At the window table",
		"items":[{"item_id":"latte","quantity":2,"option_ids":["latte-12oz"]}]
	}`)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/orders", body)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	newTestRouter(db).ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", response.Code, response.Body.String())
	}
	if db.createdRequest.CustomerName != "Maya" || db.createdOrder.SubtotalCents != 1250 {
		t.Fatalf("unexpected submitted order: request=%+v order=%+v", db.createdRequest, db.createdOrder)
	}
	if db.updateItemID != "" {
		t.Fatal("submitting an order must not update inventory")
	}
}

func TestAdminCanListSubmittedOrders(t *testing.T) {
	db := &fakeDatabase{orders: []menu.Order{{ID: "order-1", Status: "submitted"}}}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/admin/orders?status=submitted", nil)
	request.Header.Set("X-Admin-Key", "test-secret")
	response := httptest.NewRecorder()

	newTestRouter(db).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", response.Code, response.Body.String())
	}
}

func TestAdminMarksPaidOrderSold(t *testing.T) {
	db := &fakeDatabase{updatedOrder: menu.Order{ID: "order-1", Status: "sold"}}
	request := httptest.NewRequest(http.MethodPatch, "/api/v1/admin/orders/order-1/status", bytes.NewBufferString(`{"status":"sold"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Admin-Key", "test-secret")
	response := httptest.NewRecorder()

	newTestRouter(db).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", response.Code, response.Body.String())
	}
	if db.updatedOrderID != "order-1" || db.updatedStatus != "sold" {
		t.Fatalf("unexpected status update: id=%q status=%q", db.updatedOrderID, db.updatedStatus)
	}
}

func TestUpdateInventoryRequiresAdminKey(t *testing.T) {
	db := &fakeDatabase{}
	request := httptest.NewRequest(http.MethodPatch, "/api/v1/admin/items/latte/inventory", bytes.NewBufferString(`{"quantity":0}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	newTestRouter(db).ServeHTTP(response, request)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("expected status 401, got %d", response.Code)
	}
}

func TestUpdateInventory(t *testing.T) {
	db := &fakeDatabase{inventory: menu.Inventory{ItemID: "latte", Quantity: 0, Available: false}}
	request := httptest.NewRequest(http.MethodPatch, "/api/v1/admin/items/latte/inventory", bytes.NewBufferString(`{"quantity":0}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Admin-Key", "test-secret")
	response := httptest.NewRecorder()

	newTestRouter(db).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", response.Code, response.Body.String())
	}
	if db.updateItemID != "latte" || db.updateQuantity != 0 {
		t.Fatalf("unexpected update: item=%q quantity=%d", db.updateItemID, db.updateQuantity)
	}
}

func TestCreateMenuItemsAcceptsOneOrManyItems(t *testing.T) {
	tests := []struct {
		name string
		body string
		want int
	}{
		{
			name: "one item in the array",
			body: `[{"id":"carrot-cake-slice","category_id":"cakes","name":"Carrot Cake","type":"cake","price_cents":750,"currency":"usd","quantity":6}]`,
			want: 1,
		},
		{
			name: "multiple items in the array",
			body: `[
				{"id":"carrot-cake-slice","category_id":"cakes","name":"Carrot Cake","type":"cake","price_cents":750,"currency":"USD","quantity":6},
				{"id":"flat-white","category_id":"drinks","name":"Flat White","type":"drink","price_cents":525,"currency":"USD","quantity":30,
				 "modifier_groups":[{"id":"flat-white-milk","name":"Milk","min_selections":1,"max_selections":1,
				 "options":[{"id":"flat-white-whole","name":"Whole milk","price_delta_cents":0}]}]}
			]`,
			want: 2,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := &fakeDatabase{}
			request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/menu/items", bytes.NewBufferString(test.body))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("X-Admin-Key", "test-secret")
			response := httptest.NewRecorder()

			newTestRouter(db).ServeHTTP(response, request)

			if response.Code != http.StatusCreated {
				t.Fatalf("expected status 201, got %d: %s", response.Code, response.Body.String())
			}
			if len(db.createdMenuItems) != test.want {
				t.Fatalf("expected %d created items, got %d", test.want, len(db.createdMenuItems))
			}
			if db.createdMenuItems[0].Currency != "USD" {
				t.Fatalf("expected normalized currency, got %q", db.createdMenuItems[0].Currency)
			}
		})
	}
}

func TestAdminCategoryDropdownReturnsAllCategories(t *testing.T) {
	db := &fakeDatabase{categories: []menu.MenuCategory{
		{ID: "drinks", Name: "Beverages", SortOrder: 10},
		{ID: "cakes", Name: "Cakes", SortOrder: 20},
	}}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/admin/menu/categories", nil)
	request.Header.Set("X-Admin-Key", "test-secret")
	response := httptest.NewRecorder()

	newTestRouter(db).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", response.Code, response.Body.String())
	}
	var result struct {
		Categories []menu.MenuCategory `json:"categories"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Categories) != 2 || result.Categories[0].Name != "Beverages" {
		t.Fatalf("unexpected categories: %+v", result.Categories)
	}
}

func TestAdminCanCreateCustomCategories(t *testing.T) {
	db := &fakeDatabase{}
	body := bytes.NewBufferString(`[
		{"id":"cold-beverages","name":"Cold Beverages","description":"Iced drinks","sort_order":50},
		{"id":"savory","name":"Savory Bakes","sort_order":60}
	]`)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/menu/categories", body)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Admin-Key", "test-secret")
	response := httptest.NewRecorder()

	newTestRouter(db).ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", response.Code, response.Body.String())
	}
	if len(db.createdCategories) != 2 || db.createdCategories[0].ID != "cold-beverages" {
		t.Fatalf("unexpected categories: %+v", db.createdCategories)
	}
}

func TestMenuAppRedirect(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/menu", nil)
	response := httptest.NewRecorder()

	newTestRouter(&fakeDatabase{}).ServeHTTP(response, request)

	if response.Code != http.StatusTemporaryRedirect {
		t.Fatalf("expected status 307, got %d", response.Code)
	}
	if response.Header().Get("Location") != "https://menu.example.com" {
		t.Fatalf("unexpected redirect: %q", response.Header().Get("Location"))
	}
}

func TestAdminProvisionsSingleMenuQR(t *testing.T) {
	db := &fakeDatabase{}
	dubService := &fakeDub{retrieveErr: dub.ErrNotFound, link: dub.Link{
		ID: "dub-link-1", ShortLink: "https://dub.sh/menu",
		QRCode:      "https://api.dub.co/qr?url=https://dub.sh/menu",
		Destination: "https://menu.example.com",
	}}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/menu/qr", nil)
	request.Header.Set("X-Admin-Key", "test-secret")
	response := httptest.NewRecorder()

	newTestRouterWithDub(db, dubService).ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", response.Code, response.Body.String())
	}
	if db.qrLink.ID != "dub-link-1" {
		t.Fatalf("expected QR link to be saved, got %+v", db.qrLink)
	}
}

func TestMenuQRImageIsProxiedThroughBackend(t *testing.T) {
	db := &fakeDatabase{qrLink: dub.Link{
		ID: "dub-link-1", QRCode: "https://api.dub.co/qr?url=https://dub.sh/menu",
	}}
	dubService := &fakeDub{image: []byte("png-data")}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/menu/qr.png", nil)
	response := httptest.NewRecorder()

	newTestRouterWithDub(db, dubService).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", response.Code, response.Body.String())
	}
	if response.Header().Get("Content-Type") != "image/png" || response.Body.String() != "png-data" {
		t.Fatalf("unexpected QR image response: headers=%v body=%q", response.Header(), response.Body.String())
	}
}
