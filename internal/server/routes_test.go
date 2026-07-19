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
	"github.com/shohinx/vanilla-api/internal/sdk/models"
	"github.com/shohinx/vanilla-api/internal/service/dub"
	"github.com/shohinx/vanilla-api/internal/service/seaweedfs"
)

type fakeDatabase struct {
	menu               models.Menu
	includeUnavailable bool
	inventory          models.Inventory
	updateItemID       int64
	updateQuantity     int
	updatedImage       models.ItemImage
	updateImageItemID  int64
	updateImageURL     string
	updateImageErr     error
	createdOrder       models.Order
	orders             []models.Order
	updatedOrder       models.Order
	updatedOrderID     int64
	updatedStatus      string
	updatedStaffID     int64
	qrLink             models.Link
	createdRequest     models.SubmitOrderRequest
	createdMenuItems   []models.NewItem
	categories         []models.MenuCategory
	createdCategories  []models.NewCategory
	staff              []models.Staff
	staffPINHash       string
}

func (f *fakeDatabase) Initialize(context.Context) error { return nil }
func (f *fakeDatabase) Close() error                     { return nil }
func (f *fakeDatabase) Health(context.Context) map[string]string {
	return map[string]string{"status": "up"}
}
func (f *fakeDatabase) Menu(_ context.Context, includeUnavailable bool) (models.Menu, error) {
	f.includeUnavailable = includeUnavailable
	return f.menu, nil
}

func (f *fakeDatabase) Categories(context.Context) ([]models.MenuCategory, error) {
	return f.categories, nil
}

func (f *fakeDatabase) CreateCategories(_ context.Context, categories []models.NewCategory) ([]models.MenuCategory, error) {
	f.createdCategories = categories
	created := make([]models.MenuCategory, 0, len(categories))
	for _, category := range categories {
		created = append(created, models.MenuCategory{
			ID: int64(len(created) + 1), Name: category.Name, SortOrder: category.SortOrder,
		})
	}
	return created, nil
}
func (f *fakeDatabase) UpdateInventory(_ context.Context, itemID int64, quantity int) (models.Inventory, error) {
	f.updateItemID = itemID
	f.updateQuantity = quantity
	return f.inventory, nil
}

func (f *fakeDatabase) UpdateVariantInventory(_ context.Context, variantID int64, quantity int) (models.VariantInventory, error) {
	return models.VariantInventory{VariantOptionID: variantID, Quantity: quantity, Available: quantity > 0}, nil
}

func (f *fakeDatabase) UpdateItemAvailability(_ context.Context, itemID int64, available bool) (models.ItemAvailability, error) {
	return models.ItemAvailability{ItemID: itemID, Available: available}, nil
}

func (f *fakeDatabase) UpdateItemImage(_ context.Context, itemID int64, imageURL string) (models.ItemImage, error) {
	f.updateImageItemID = itemID
	f.updateImageURL = imageURL
	if f.updateImageErr != nil {
		return models.ItemImage{}, f.updateImageErr
	}
	f.updatedImage.ItemID = itemID
	f.updatedImage.ImageURL = imageURL
	return f.updatedImage, nil
}

func (f *fakeDatabase) CreateMenuItems(_ context.Context, items []models.NewItem) ([]models.Item, error) {
	f.createdMenuItems = items
	created := make([]models.Item, 0, len(items))
	for _, item := range items {
		imageURL := ""
		if item.ImageURL != nil {
			imageURL = *item.ImageURL
		}
		created = append(created, models.Item{
			ID: int64(len(created) + 1), Name: item.Name, ImageURL: imageURL, PriceCents: item.PriceCents,
		})
	}
	return created, nil
}

func (f *fakeDatabase) CreateOrder(_ context.Context, request models.SubmitOrderRequest, quote models.Quote) (models.Order, error) {
	f.createdRequest = request
	f.createdOrder.Items = quote.Items
	f.createdOrder.TotalCents = quote.TotalCents
	return f.createdOrder, nil
}

func (f *fakeDatabase) Orders(context.Context, string) ([]models.Order, error) {
	return f.orders, nil
}

func (f *fakeDatabase) UpdateOrderStatus(_ context.Context, orderID int64, status string, staffID int64) (models.Order, error) {
	f.updatedOrderID = orderID
	f.updatedStatus = status
	f.updatedStaffID = staffID
	return f.updatedOrder, nil
}

func (f *fakeDatabase) Staff(context.Context, bool) ([]models.Staff, error) { return f.staff, nil }
func (f *fakeDatabase) StaffActive(_ context.Context, staffID int64) (bool, error) {
	for _, member := range f.staff {
		if member.ID == staffID {
			return member.Active, nil
		}
	}
	return false, database.ErrNotFound
}
func (f *fakeDatabase) StaffCredentials(_ context.Context, name string) (models.Staff, string, error) {
	for _, member := range f.staff {
		if member.Name == name {
			return member, f.staffPINHash, nil
		}
	}
	return models.Staff{}, "", database.ErrNotFound
}
func (f *fakeDatabase) CreateStaff(_ context.Context, name, _ string) (models.Staff, error) {
	member := models.Staff{ID: int64(len(f.staff) + 1), Name: name, Active: true}
	f.staff = append(f.staff, member)
	return member, nil
}
func (f *fakeDatabase) SetStaffActive(_ context.Context, staffID int64, active bool) (models.Staff, error) {
	return models.Staff{ID: staffID, Name: "Worker", Active: active}, nil
}

func (f *fakeDatabase) MenuQR(context.Context) (models.Link, error) {
	if f.qrLink.ID == "" {
		return models.Link{}, database.ErrNotFound
	}
	return f.qrLink, nil
}

func (f *fakeDatabase) SaveMenuQR(_ context.Context, link models.Link) error {
	f.qrLink = link
	return nil
}

type fakeDub struct {
	link        models.Link
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
	getObject      seaweedfs.Object
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

func (f *fakeImageStore) Get(_ context.Context, key string) (seaweedfs.Object, error) {
	f.getKey = key
	return f.getObject, f.getErr
}

func (f *fakeDub) CreateMenuLink(context.Context, string, string, string) (models.Link, error) {
	return f.link, nil
}

func (f *fakeDub) RetrieveMenuLink(context.Context, string, string, string) (models.Link, error) {
	if f.retrieveErr != nil {
		return models.Link{}, f.retrieveErr
	}
	if f.link.ID == "" {
		return models.Link{}, dub.ErrNotFound
	}
	return f.link, nil
}

func (f *fakeDub) QRCode(context.Context, string) ([]byte, error) {
	return f.image, nil
}

func testMenu() models.Menu {
	return models.Menu{
		GeneratedAt: time.Date(2026, time.July, 16, 12, 0, 0, 0, time.UTC),
		Categories: []models.Category{{
			ID: 1, Name: "Drinks", Items: []models.Item{{
				ID: 1, Name: "Latte", PriceCents: 550, Available: true,
				VariantGroups: []models.VariantGroup{{
					ID: 10, Name: "Size", Required: true,
					Options: []models.VariantOption{
						{ID: 11, Name: "Small", PriceCents: 550, Available: true},
						{ID: 12, Name: "Large", PriceCents: 625, Available: true},
					},
				}},
			}},
		}},
	}
}

func newTestRouter(db *fakeDatabase) http.Handler {
	return New(db, &fakeDub{}, nil, Config{
		AdminAPIKey:    "test-secret",
		MenuAppURL:     "https://models.example.com",
		AllowedOrigins: []string{"http://localhost:5173"},
	}).RegisterRoutes()
}

func newTestRouterWithDub(db *fakeDatabase, dubService *fakeDub) http.Handler {
	return New(db, dubService, nil, Config{
		AdminAPIKey:    "test-secret",
		MenuAppURL:     "https://models.example.com",
		AllowedOrigins: []string{"http://localhost:5173"},
		DubAPIKey:      "dub_test",
		DubDomain:      "dub.sh",
		DubLinkKey:     "diAI31C",
	}).RegisterRoutes()
}

func newTestRouterWithImages(db *fakeDatabase, imageService seaweedfs.Service) http.Handler {
	return New(db, &fakeDub{}, imageService, Config{
		AdminAPIKey:    "test-secret",
		MenuAppURL:     "https://models.example.com",
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
	imageService := &fakeImageStore{getObject: seaweedfs.Object{
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
	body := bytes.NewBufferString(`{"items":[{"item_id":1,"quantity":2,"variant_option_id":12}]}`)
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
	var quote models.Quote
	if err := json.Unmarshal(response.Body.Bytes(), &quote); err != nil {
		t.Fatal(err)
	}
	if quote.TotalCents != 1250 {
		t.Fatalf("expected total 1250, got %d", quote.TotalCents)
	}
}

func TestQuoteHandlerRejectsMissingRequiredCustomization(t *testing.T) {
	db := &fakeDatabase{menu: testMenu()}
	body := bytes.NewBufferString(`{"items":[{"item_id":1,"quantity":1}]}`)
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
		createdOrder: models.Order{ID: 1, OrderNumber: "VL-123456", Status: "new"},
	}
	body := bytes.NewBufferString(`{
		"table_number":"Window 3",
		"guest_count":4,
		"items":[{"item_id":1,"quantity":2,"variant_option_id":12}]
	}`)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/orders", body)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	newTestRouter(db).ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", response.Code, response.Body.String())
	}
	if db.createdRequest.TableNumber != "Window 3" || db.createdRequest.GuestCount != 4 || db.createdOrder.TotalCents != 1250 {
		t.Fatalf("unexpected submitted order: request=%+v order=%+v", db.createdRequest, db.createdOrder)
	}
	if db.updateItemID != 0 {
		t.Fatal("submitting an order must not update inventory")
	}
}

func TestSubmitOrderRequiresTableNumberAndGuestCount(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "missing table", body: `{"guest_count":2,"items":[{"item_id":1,"quantity":1,"variant_option_id":11}]}`},
		{name: "missing guests", body: `{"table_number":"4","items":[{"item_id":1,"quantity":1,"variant_option_id":11}]}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/api/v1/orders", bytes.NewBufferString(test.body))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			newTestRouter(&fakeDatabase{menu: testMenu()}).ServeHTTP(response, request)
			if response.Code != http.StatusUnprocessableEntity {
				t.Fatalf("expected status 422, got %d: %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestAdminCanListNewOrders(t *testing.T) {
	db := &fakeDatabase{orders: []models.Order{{ID: 1, Status: "new"}}}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/admin/orders?status=new", nil)
	request.Header.Set("X-Admin-Key", "test-secret")
	response := httptest.NewRecorder()

	newTestRouter(db).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", response.Code, response.Body.String())
	}
}

func TestAdminMarksOrderSoldAsStaff(t *testing.T) {
	db := &fakeDatabase{updatedOrder: models.Order{ID: 1, Status: "sold"}}
	request := httptest.NewRequest(http.MethodPatch, "/api/v1/admin/orders/1/status", bytes.NewBufferString(`{"status":"sold","staff_id":9}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Admin-Key", "test-secret")
	response := httptest.NewRecorder()

	newTestRouter(db).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", response.Code, response.Body.String())
	}
	if db.updatedOrderID != 1 || db.updatedStatus != "sold" || db.updatedStaffID != 9 {
		t.Fatalf("unexpected status update: id=%d status=%q staff=%d", db.updatedOrderID, db.updatedStatus, db.updatedStaffID)
	}
}

func TestStaffLoginTokenAttributesOrderAction(t *testing.T) {
	pinHash, err := hashPIN("4826")
	if err != nil {
		t.Fatal(err)
	}
	db := &fakeDatabase{
		staff: []models.Staff{{ID: 7, Name: "Maya", Active: true}}, staffPINHash: pinHash,
		updatedOrder: models.Order{ID: 1, Status: "sold"},
	}
	loginRequest := httptest.NewRequest(http.MethodPost, "/api/v1/staff/login", bytes.NewBufferString(`{"name":"Maya","pin":"4826"}`))
	loginRequest.Header.Set("Content-Type", "application/json")
	loginResponse := httptest.NewRecorder()
	newTestRouter(db).ServeHTTP(loginResponse, loginRequest)
	if loginResponse.Code != http.StatusOK {
		t.Fatalf("expected login status 200, got %d: %s", loginResponse.Code, loginResponse.Body.String())
	}
	var login struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(loginResponse.Body.Bytes(), &login); err != nil || login.AccessToken == "" {
		t.Fatalf("unexpected login response: body=%s err=%v", loginResponse.Body.String(), err)
	}

	statusRequest := httptest.NewRequest(http.MethodPatch, "/api/v1/admin/orders/1/status", bytes.NewBufferString(`{"status":"sold"}`))
	statusRequest.Header.Set("Content-Type", "application/json")
	statusRequest.Header.Set("Authorization", "Bearer "+login.AccessToken)
	statusResponse := httptest.NewRecorder()
	newTestRouter(db).ServeHTTP(statusResponse, statusRequest)
	if statusResponse.Code != http.StatusOK || db.updatedStaffID != 7 {
		t.Fatalf("expected staff-attributed sale, status=%d staff=%d body=%s", statusResponse.Code, db.updatedStaffID, statusResponse.Body.String())
	}
}

func TestUpdateInventoryRequiresAdminKey(t *testing.T) {
	db := &fakeDatabase{}
	request := httptest.NewRequest(http.MethodPatch, "/api/v1/admin/items/1/inventory", bytes.NewBufferString(`{"stock_qty":0}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	newTestRouter(db).ServeHTTP(response, request)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("expected status 401, got %d", response.Code)
	}
}

func TestUpdateInventory(t *testing.T) {
	db := &fakeDatabase{inventory: models.Inventory{ItemID: 1, Quantity: 0, Available: false}}
	request := httptest.NewRequest(http.MethodPatch, "/api/v1/admin/items/1/inventory", bytes.NewBufferString(`{"stock_qty":0}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Admin-Key", "test-secret")
	response := httptest.NewRecorder()

	newTestRouter(db).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", response.Code, response.Body.String())
	}
	if db.updateItemID != 1 || db.updateQuantity != 0 {
		t.Fatalf("unexpected update: item=%d quantity=%d", db.updateItemID, db.updateQuantity)
	}
}

func TestUpdateItemImage(t *testing.T) {
	db := &fakeDatabase{}
	request := httptest.NewRequest(http.MethodPatch, "/api/v1/admin/items/2/image", bytes.NewBufferString(`{"image_url":" https://api.example.com/api/v1/images/menu/new.jpg "}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Admin-Key", "test-secret")
	response := httptest.NewRecorder()

	newTestRouter(db).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", response.Code, response.Body.String())
	}
	if db.updateImageItemID != 2 || db.updateImageURL != "https://api.example.com/api/v1/images/menu/new.jpg" {
		t.Fatalf("unexpected image update: item=%d image_url=%q", db.updateImageItemID, db.updateImageURL)
	}
	var result models.ItemImage
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.ItemID != 2 || result.ImageURL != db.updateImageURL {
		t.Fatalf("unexpected response: %+v", result)
	}
}

func TestUpdateItemImageRequiresAdminKey(t *testing.T) {
	db := &fakeDatabase{}
	request := httptest.NewRequest(http.MethodPatch, "/api/v1/admin/items/2/image", bytes.NewBufferString(`{"image_url":"/api/v1/images/menu/new.jpg"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	newTestRouter(db).ServeHTTP(response, request)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("expected status 401, got %d", response.Code)
	}
	if db.updateImageItemID != 0 {
		t.Fatal("database should not be called without an admin key")
	}
}

func TestUpdateItemImageRequiresImageURLField(t *testing.T) {
	db := &fakeDatabase{}
	request := httptest.NewRequest(http.MethodPatch, "/api/v1/admin/items/2/image", bytes.NewBufferString(`{}`))
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
	request := httptest.NewRequest(http.MethodPatch, "/api/v1/admin/items/2/image", bytes.NewBufferString(`{"image_url":""}`))
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
	request := httptest.NewRequest(http.MethodPatch, "/api/v1/admin/items/2/image", bytes.NewBufferString(`{"image_url":"not-a-url"}`))
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
	request := httptest.NewRequest(http.MethodPatch, "/api/v1/admin/items/999/image", bytes.NewBufferString(`{"image_url":"/api/v1/images/menu/new.jpg"}`))
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
			body: `[{"category_id":2,"name":"Carrot Cake","price_cents":750,"track_stock":true,"stock_qty":6}]`,
			want: 1,
		},
		{
			name: "multiple items in the array",
			body: `[
				{"category_id":2,"name":"Carrot Cake","price_cents":750,"track_stock":true,"stock_qty":6},
				{"category_id":4,"name":"Flat White","price_cents":525,"track_stock":false,
				 "variant_group":{"name":"Size","options":[
				 {"name":"Small","price_cents":525,"track_stock":false},
				 {"name":"Large","price_cents":600,"track_stock":false}]}}
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
			if !db.createdMenuItems[0].TrackStock || db.createdMenuItems[0].StockQuantity == nil {
				t.Fatalf("expected tracked stock to be retained: %+v", db.createdMenuItems[0])
			}
			if db.createdMenuItems[0].ImageURL != nil {
				t.Fatalf("expected image_url to be optional, got %q", *db.createdMenuItems[0].ImageURL)
			}
		})
	}
}

func TestCreateMenuItemsAcceptsAnOptionalImageURL(t *testing.T) {
	db := &fakeDatabase{}
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/admin/menu/items",
		bytes.NewBufferString(`[{"category_id":3,"name":"Cookie","image_url":" /api/v1/images/menu/cookie.webp ","price_cents":350,"track_stock":true,"stock_qty":4}]`),
	)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Admin-Key", "test-secret")
	response := httptest.NewRecorder()

	newTestRouter(db).ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", response.Code, response.Body.String())
	}
	if len(db.createdMenuItems) != 1 || db.createdMenuItems[0].ImageURL == nil {
		t.Fatal("expected the supplied image URL to be retained")
	}
	if *db.createdMenuItems[0].ImageURL != "/api/v1/images/menu/cookie.webp" {
		t.Fatalf("unexpected image URL %q", *db.createdMenuItems[0].ImageURL)
	}
}

func TestAdminCategoryDropdownReturnsAllCategories(t *testing.T) {
	db := &fakeDatabase{categories: []models.MenuCategory{
		{ID: 1, Name: "Beverages", SortOrder: 10},
		{ID: 2, Name: "Cakes", SortOrder: 20},
	}}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/admin/menu/categories", nil)
	request.Header.Set("X-Admin-Key", "test-secret")
	response := httptest.NewRecorder()

	newTestRouter(db).ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d: %s", response.Code, response.Body.String())
	}
	var result struct {
		Categories []models.MenuCategory `json:"categories"`
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
		{"name":"Cold Beverages","sort_order":50},
		{"name":"Savory Bakes","sort_order":60}
	]`)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/menu/categories", body)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Admin-Key", "test-secret")
	response := httptest.NewRecorder()

	newTestRouter(db).ServeHTTP(response, request)

	if response.Code != http.StatusCreated {
		t.Fatalf("expected status 201, got %d: %s", response.Code, response.Body.String())
	}
	if len(db.createdCategories) != 2 || db.createdCategories[0].Name != "Cold Beverages" {
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
	if response.Header().Get("Location") != "https://models.example.com" {
		t.Fatalf("unexpected redirect: %q", response.Header().Get("Location"))
	}
}

func TestAdminProvisionsSingleMenuQR(t *testing.T) {
	db := &fakeDatabase{}
	dubService := &fakeDub{retrieveErr: dub.ErrNotFound, link: models.Link{
		ID: "dub-link-1", ShortLink: "https://dub.sh/menu",
		QRCode:      "https://api.dub.co/qr?url=https://dub.sh/menu",
		Destination: "https://models.example.com",
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
	db := &fakeDatabase{qrLink: models.Link{
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
