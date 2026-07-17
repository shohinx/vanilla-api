package server

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/shohinx/vanilla-api/internal/database"
	"github.com/shohinx/vanilla-api/internal/dub"
	"github.com/shohinx/vanilla-api/internal/imagestore"
	"github.com/shohinx/vanilla-api/internal/menu"
)

type fakeDatabase struct {
	menu               menu.Menu
	includeUnavailable bool
	inventory          menu.Inventory
	updateItemID       string
	updateQuantity     int
	updatedImage       menu.ItemImage
	updateImageItemID  string
	updateImageURL     string
	updateImageErr     error
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

func (f *fakeDatabase) UpdateItemImage(_ context.Context, itemID, imageURL string) (menu.ItemImage, error) {
	f.updateImageItemID = itemID
	f.updateImageURL = imageURL
	if f.updateImageErr != nil {
		return menu.ItemImage{}, f.updateImageErr
	}
	f.updatedImage.ItemID = itemID
	f.updatedImage.ImageURL = imageURL
	return f.updatedImage, nil
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

type fakeImageStore struct {
	putKey         string
	putData        []byte
	putSize        int64
	putContentType string
	putErr         error
	getKey         string
	getObject      imagestore.Object
	getErr         error
}

func (f *fakeImageStore) Put(_ context.Context, key string, body io.Reader, size int64, contentType string) error {
	f.putKey = key
	f.putSize = size
	f.putContentType = contentType
	if f.putErr != nil {
		return f.putErr
	}
	f.putData, _ = io.ReadAll(body)
	return nil
}

func (f *fakeImageStore) Get(_ context.Context, key string) (imagestore.Object, error) {
	f.getKey = key
	return f.getObject, f.getErr
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
	return New(db, &fakeDub{}, nil, Config{
		AdminAPIKey:    "test-secret",
		MenuAppURL:     "https://menu.example.com",
		AllowedOrigins: []string{"http://localhost:5173"},
	}).RegisterRoutes()
}

func newTestRouterWithDub(db *fakeDatabase, dubService *fakeDub) http.Handler {
	return New(db, dubService, nil, Config{
		AdminAPIKey:    "test-secret",
		MenuAppURL:     "https://menu.example.com",
		AllowedOrigins: []string{"http://localhost:5173"},
		DubAPIKey:      "dub_test",
		DubLinkKey:     "menu",
	}).RegisterRoutes()
}

func newTestRouterWithImages(db *fakeDatabase, imageService imagestore.Service) http.Handler {
	return New(db, &fakeDub{}, imageService, Config{
		AdminAPIKey:    "test-secret",
		MenuAppURL:     "https://menu.example.com",
		AllowedOrigins: []string{"http://localhost:5173"},
		PublicBaseURL:  "https://api.example.com",
	}).RegisterRoutes()
}

func imageUploadBody(t *testing.T, filename string, contents []byte) (*bytes.Buffer, string) {
	t.Helper()
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(contents); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return body, writer.FormDataContentType()
}

func pngImage(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.SetNRGBA(x, y, color.NRGBA{
				R: uint8((x * 255) / width),
				G: uint8((y * 255) / height),
				B: 120,
				A: 255,
			})
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, img); err != nil {
		t.Fatal(err)
	}
	return encoded.Bytes()
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

func TestAdminUploadsImageToObjectStorage(t *testing.T) {
	imageService := &fakeImageStore{}
	imageData := pngImage(t, 64, 32)
	body, contentType := imageUploadBody(t, "cake.png", imageData)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/images", body)
	request.Header.Set("Content-Type", contentType)
	request.Header.Set("X-Admin-Key", "test-secret")
	response := httptest.NewRecorder()

	newTestRouterWithImages(&fakeDatabase{}, imageService).ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", response.Code, response.Body.String())
	}
	if !validImageKey(imageService.putKey) || !strings.HasSuffix(imageService.putKey, ".webp") {
		t.Fatalf("unexpected object key %q", imageService.putKey)
	}
	if imageService.putContentType != "image/webp" || imageService.putSize != int64(len(imageService.putData)) {
		t.Fatalf("unexpected stored image: type=%q size=%d", imageService.putContentType, imageService.putSize)
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(imageService.putData))
	if err != nil {
		t.Fatalf("stored image is not decodable: %v", err)
	}
	if format != "webp" || config.Width != 64 || config.Height != 32 {
		t.Fatalf("unexpected optimized image: format=%q dimensions=%dx%d", format, config.Width, config.Height)
	}
	var result struct {
		ImageURL     string `json:"image_url"`
		ContentType  string `json:"content_type"`
		Size         int64  `json:"size"`
		OriginalSize int64  `json:"original_size"`
		Width        int    `json:"width"`
		Height       int    `json:"height"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.ImageURL != "https://api.example.com/api/v1/images/"+imageService.putKey {
		t.Fatalf("unexpected image URL %q", result.ImageURL)
	}
	if result.ContentType != "image/webp" || result.Size != imageService.putSize || result.OriginalSize != int64(len(imageData)) || result.Width != 64 || result.Height != 32 {
		t.Fatalf("unexpected upload response: %+v", result)
	}
}

func TestAdminImageUploadRejectsMalformedImage(t *testing.T) {
	imageService := &fakeImageStore{}
	imageData := append([]byte("\x89PNG\r\n\x1a\n"), []byte("not-a-valid-png")...)
	body, contentType := imageUploadBody(t, "broken.png", imageData)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/images", body)
	request.Header.Set("Content-Type", contentType)
	request.Header.Set("X-Admin-Key", "test-secret")
	response := httptest.NewRecorder()

	newTestRouterWithImages(&fakeDatabase{}, imageService).ServeHTTP(response, request)

	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected status 422, got %d: %s", response.Code, response.Body.String())
	}
	if imageService.putKey != "" {
		t.Fatal("malformed image must not be uploaded")
	}
}

func TestAdminImageUploadRejectsNonImage(t *testing.T) {
	imageService := &fakeImageStore{}
	body, contentType := imageUploadBody(t, "notes.txt", []byte("not an image"))
	request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/images", body)
	request.Header.Set("Content-Type", contentType)
	request.Header.Set("X-Admin-Key", "test-secret")
	response := httptest.NewRecorder()

	newTestRouterWithImages(&fakeDatabase{}, imageService).ServeHTTP(response, request)

	if response.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("expected status 415, got %d: %s", response.Code, response.Body.String())
	}
	if imageService.putKey != "" {
		t.Fatal("unsupported content must not be uploaded")
	}
}

func TestPublicImageIsProxiedFromObjectStorage(t *testing.T) {
	key := "menu/0123456789abcdef0123456789abcdef.jpg"
	imageService := &fakeImageStore{getObject: imagestore.Object{
		Body:          io.NopCloser(strings.NewReader("jpeg-data")),
		ContentType:   "image/jpeg",
		ContentLength: int64(len("jpeg-data")),
		ETag:          "image-etag",
	}}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/images/"+key, nil)
	response := httptest.NewRecorder()

	newTestRouterWithImages(&fakeDatabase{}, imageService).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", response.Code, response.Body.String())
	}
	if imageService.getKey != key || response.Body.String() != "jpeg-data" {
		t.Fatalf("unexpected proxied image: key=%q body=%q", imageService.getKey, response.Body.String())
	}
	if response.Header().Get("Content-Type") != "image/jpeg" || response.Header().Get("ETag") != "\"image-etag\"" {
		t.Fatalf("unexpected headers: %v", response.Header())
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

func TestUpdateItemImage(t *testing.T) {
	db := &fakeDatabase{}
	request := httptest.NewRequest(http.MethodPatch, "/api/v1/admin/items/carrot-cake/image", bytes.NewBufferString(`{"image_url":" https://api.example.com/api/v1/images/menu/new.jpg "}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Admin-Key", "test-secret")
	response := httptest.NewRecorder()

	newTestRouter(db).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", response.Code, response.Body.String())
	}
	if db.updateImageItemID != "carrot-cake" || db.updateImageURL != "https://api.example.com/api/v1/images/menu/new.jpg" {
		t.Fatalf("unexpected image update: item=%q image_url=%q", db.updateImageItemID, db.updateImageURL)
	}
	var result menu.ItemImage
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.ItemID != "carrot-cake" || result.ImageURL != db.updateImageURL {
		t.Fatalf("unexpected response: %+v", result)
	}
}

func TestUpdateItemImageRequiresAdminKey(t *testing.T) {
	db := &fakeDatabase{}
	request := httptest.NewRequest(http.MethodPatch, "/api/v1/admin/items/carrot-cake/image", bytes.NewBufferString(`{"image_url":"/api/v1/images/menu/new.jpg"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	newTestRouter(db).ServeHTTP(response, request)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("expected status 401, got %d", response.Code)
	}
	if db.updateImageItemID != "" {
		t.Fatal("database should not be called without an admin key")
	}
}

func TestUpdateItemImageRequiresImageURLField(t *testing.T) {
	db := &fakeDatabase{}
	request := httptest.NewRequest(http.MethodPatch, "/api/v1/admin/items/carrot-cake/image", bytes.NewBufferString(`{}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Admin-Key", "test-secret")
	response := httptest.NewRecorder()

	newTestRouter(db).ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d: %s", response.Code, response.Body.String())
	}
}

func TestUpdateItemImageCanClearImage(t *testing.T) {
	db := &fakeDatabase{}
	request := httptest.NewRequest(http.MethodPatch, "/api/v1/admin/items/carrot-cake/image", bytes.NewBufferString(`{"image_url":""}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Admin-Key", "test-secret")
	response := httptest.NewRecorder()

	newTestRouter(db).ServeHTTP(response, request)

	if response.Code != http.StatusOK || db.updateImageURL != "" {
		t.Fatalf("expected image to be cleared, got status=%d image_url=%q", response.Code, db.updateImageURL)
	}
}

func TestUpdateItemImageRejectsInvalidURL(t *testing.T) {
	db := &fakeDatabase{}
	request := httptest.NewRequest(http.MethodPatch, "/api/v1/admin/items/carrot-cake/image", bytes.NewBufferString(`{"image_url":"not-a-url"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Admin-Key", "test-secret")
	response := httptest.NewRecorder()

	newTestRouter(db).ServeHTTP(response, request)

	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected status 422, got %d: %s", response.Code, response.Body.String())
	}
}

func TestUpdateItemImageReturnsNotFound(t *testing.T) {
	db := &fakeDatabase{updateImageErr: database.ErrNotFound}
	request := httptest.NewRequest(http.MethodPatch, "/api/v1/admin/items/missing/image", bytes.NewBufferString(`{"image_url":"/api/v1/images/menu/new.jpg"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Admin-Key", "test-secret")
	response := httptest.NewRecorder()

	newTestRouter(db).ServeHTTP(response, request)

	if response.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d: %s", response.Code, response.Body.String())
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
