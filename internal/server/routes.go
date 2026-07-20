package server

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"

	"github.com/shohinx/vanilla-api/internal/database"
	"github.com/shohinx/vanilla-api/internal/menu"
	"github.com/shohinx/vanilla-api/internal/sdk/models"
	"github.com/shohinx/vanilla-api/internal/service/dub"
	"github.com/shohinx/vanilla-api/internal/service/seaweedfs"
)

const maxImageBytes int64 = 50 << 20

const optimizedImageExtension = ".webp"

// dummyPINHash keeps failed staff lookups on the same PBKDF2 path as valid
// users, reducing username-enumeration timing differences.
const dummyPINHash = "pbkdf2_sha256$210000$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

func (s *Server) RegisterRoutes() http.Handler {
	router := gin.New()
	router.Use(s.requestLogger(), s.recoverer(), requestBodyLimit())
	if len(s.config.AllowedOrigins) != 0 {
		corsConfig := corsConfiguration(s.config.AllowedOrigins)
		if err := corsConfig.Validate(); err != nil {
			s.logger.Error("disable invalid CORS configuration", "error", err)
		} else {
			router.Use(cors.New(corsConfig))
		}
	}

	router.GET("/", s.serviceHandler)
	router.GET("/health", s.healthHandler)
	router.GET("/menu", s.menuAppHandler)

	api := router.Group("/api/v1")
	api.GET("/menu", s.menuHandler)
	api.GET("/menu/qr", s.menuQRHandler)
	api.GET("/menu/qr.png", s.menuQRImageHandler)
	api.GET("/images/*key", s.imageHandler)
	api.POST("/order-plans/quote", s.quoteHandler)
	api.POST("/orders", s.submitOrderHandler)
	api.POST("/staff/login", s.staffLoginHandler)

	admin := api.Group("/admin")
	admin.Use(s.requireAdminKey())
	admin.PATCH("/items/:id/inventory", s.updateInventoryHandler)
	admin.PATCH("/variants/:id/inventory", s.updateVariantInventoryHandler)
	admin.PATCH("/items/:id/availability", s.updateItemAvailabilityHandler)
	admin.PATCH("/items/:id/image", s.updateItemImageHandler)
	admin.GET("/menu/categories", s.menuCategoriesHandler)
	admin.POST("/menu/categories", s.createMenuCategoriesHandler)
	admin.POST("/menu/items", s.createMenuItemsHandler)
	admin.POST("/images", s.uploadImageHandler)
	admin.GET("/orders", s.adminOrdersHandler)
	admin.PATCH("/orders/:id/status", s.updateOrderStatusHandler)
	admin.GET("/staff", s.staffHandler)
	admin.POST("/staff", s.createStaffHandler)
	admin.PATCH("/staff/:id/active", s.updateStaffActiveHandler)
	admin.POST("/menu/qr", s.provisionMenuQRHandler)

	return router
}

func corsConfiguration(allowedOrigins []string) cors.Config {
	return cors.Config{
		AllowOrigins:     allowedOrigins,
		AllowMethods:     []string{"GET", "POST", "PATCH", "OPTIONS"},
		AllowHeaders:     []string{"Accept", "Authorization", "Content-Type", "X-Admin-Key"},
		AllowCredentials: false,
	}
}

func (s *Server) uploadImageHandler(c *gin.Context) {
	if s.images == nil {
		writeError(c, http.StatusServiceUnavailable, "image_storage_not_configured", "image storage is not configured", nil)
		return
	}

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxImageBytes+(1<<20))
	fileHeader, err := c.FormFile("file")
	if err != nil {
		var sizeError *http.MaxBytesError
		if errors.As(err, &sizeError) {
			writeError(c, http.StatusRequestEntityTooLarge, "image_too_large", "source images may not exceed 50 MiB", nil)
			return
		}
		writeError(c, http.StatusBadRequest, "invalid_image_upload", "a multipart file field named file is required", nil)
		return
	}
	if fileHeader.Size <= 0 {
		writeError(c, http.StatusUnprocessableEntity, "empty_image", "the uploaded image is empty", nil)
		return
	}
	if fileHeader.Size > maxImageBytes {
		writeError(c, http.StatusRequestEntityTooLarge, "image_too_large", "source images may not exceed 50 MiB", nil)
		return
	}

	file, err := fileHeader.Open()
	if err != nil {
		writeError(c, http.StatusBadRequest, "invalid_image_upload", "the uploaded image could not be opened", nil)
		return
	}
	defer func() { reportError(c, file.Close()) }()

	var header [512]byte
	headerSize, err := io.ReadFull(file, header[:])
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		writeError(c, http.StatusBadRequest, "invalid_image_upload", "the uploaded image could not be read", nil)
		return
	}
	contentType := http.DetectContentType(header[:headerSize])
	if !supportedImageContentType(contentType) {
		writeError(c, http.StatusUnsupportedMediaType, "unsupported_image_type", "only JPEG, PNG, and WebP images are supported", nil)
		return
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_image_upload", "the uploaded image could not be read", nil)
		return
	}
	optimized, err := seaweedfs.Optimize(file)
	if errors.Is(err, seaweedfs.ErrTooManyPixels) {
		writeError(c, http.StatusUnprocessableEntity, "image_dimensions_too_large", "images may not exceed 40 megapixels", nil)
		return
	}
	if errors.Is(err, seaweedfs.ErrInvalidImage) {
		writeError(c, http.StatusUnprocessableEntity, "invalid_image", "the uploaded file is not a valid JPEG, PNG, or WebP image", nil)
		return
	}
	if errors.Is(err, seaweedfs.ErrCannotCompress) {
		writeError(c, http.StatusUnprocessableEntity, "image_could_not_be_compressed", "the image could not be compressed below 2 MiB", nil)
		return
	}
	if err != nil {
		writeFailure(c, http.StatusInternalServerError, "image_optimization_failed", "the image could not be optimized", err)
		return
	}

	key, err := newImageKey()
	if err != nil {
		writeFailure(c, http.StatusInternalServerError, "image_key_failed", "an image key could not be generated", err)
		return
	}
	optimizedSize := int64(len(optimized.Data))
	if err := s.images.Put(c.Request.Context(), key, bytes.NewReader(optimized.Data), optimizedSize, "image/webp"); err != nil {
		writeFailure(c, http.StatusBadGateway, "image_upload_failed", "the image could not be stored", err)
		return
	}

	imagePath := "/api/v1/images/" + key
	imageURL := imagePath
	if s.config.PublicBaseURL != "" {
		imageURL = s.config.PublicBaseURL + imagePath
	}
	c.JSON(http.StatusCreated, gin.H{
		"key":             key,
		"image_url":       imageURL,
		"content_type":    "image/webp",
		"size":            optimizedSize,
		"original_size":   fileHeader.Size,
		"original_width":  optimized.OriginalWidth,
		"original_height": optimized.OriginalHeight,
		"width":           optimized.Width,
		"height":          optimized.Height,
	})
}

func (s *Server) imageHandler(c *gin.Context) {
	if s.images == nil {
		writeError(c, http.StatusServiceUnavailable, "image_storage_not_configured", "image storage is not configured", nil)
		return
	}
	key := strings.TrimPrefix(c.Param("key"), "/")
	if !validImageKey(key) {
		writeError(c, http.StatusNotFound, "image_not_found", "image was not found", nil)
		return
	}

	object, err := s.images.Get(c.Request.Context(), key)
	if err != nil {
		if errors.Is(err, seaweedfs.ErrNotFound) {
			writeError(c, http.StatusNotFound, "image_not_found", "image was not found", nil)
			return
		}
		writeFailure(c, http.StatusBadGateway, "image_unavailable", "the image could not be loaded", err)
		return
	}
	defer func() { reportError(c, object.Body.Close()) }()

	headers := map[string]string{"Cache-Control": "public, max-age=31536000, immutable"}
	if object.ETag != "" {
		headers["ETag"] = "\"" + object.ETag + "\""
	}
	c.DataFromReader(http.StatusOK, object.ContentLength, object.ContentType, object.Body, headers)
}

func supportedImageContentType(contentType string) bool {
	switch contentType {
	case "image/jpeg", "image/png", "image/webp":
		return true
	default:
		return false
	}
}

func newImageKey() (string, error) {
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return "menu/" + hex.EncodeToString(random) + optimizedImageExtension, nil
}

func validImageKey(key string) bool {
	if !strings.HasPrefix(key, "menu/") {
		return false
	}
	name := strings.TrimPrefix(key, "menu/")
	if strings.Contains(name, "/") {
		return false
	}
	for _, extension := range []string{".jpg", ".png", ".webp"} {
		if strings.HasSuffix(name, extension) {
			encoded := strings.TrimSuffix(name, extension)
			decoded, err := hex.DecodeString(encoded)
			return err == nil && len(decoded) == 16
		}
	}
	return false
}

func (s *Server) serviceHandler(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"name":    "vanilla-api",
		"version": "v1",
		"menu":    "/api/v1/menu",
	})
}

func (s *Server) healthHandler(c *gin.Context) {
	health, err := s.db.Health(c.Request.Context())
	status := http.StatusOK
	if err != nil || health["status"] != "up" {
		status = http.StatusServiceUnavailable
		reportError(c, err)
	}
	c.JSON(status, health)
}

func (s *Server) menuAppHandler(c *gin.Context) {
	if s.config.MenuAppURL == "" {
		writeError(c, http.StatusServiceUnavailable, "menu_app_not_configured", "MENU_APP_URL is not configured", nil)
		return
	}
	c.Redirect(http.StatusTemporaryRedirect, s.config.MenuAppURL)
}

func (s *Server) menuHandler(c *gin.Context) {
	current, err := s.db.Menu(c.Request.Context(), false)
	if err != nil {
		writeFailure(c, http.StatusInternalServerError, "menu_unavailable", "the live menu could not be loaded", err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, current)
}

func (s *Server) quoteHandler(c *gin.Context) {
	var request models.QuoteRequest
	if !bindJSON(c, &request, "request body must be valid JSON") {
		return
	}

	current, err := s.db.Menu(c.Request.Context(), true)
	if err != nil {
		writeFailure(c, http.StatusInternalServerError, "menu_unavailable", "the live menu could not be loaded", err)
		return
	}
	quote, err := menu.BuildQuote(current, request)
	if err != nil {
		var validationErrors models.ValidationErrors
		if errors.As(err, &validationErrors) {
			writeError(c, http.StatusUnprocessableEntity, "invalid_order_plan", err.Error(), validationErrors)
			return
		}
		writeFailure(c, http.StatusInternalServerError, "quote_failed", "the order plan could not be priced", err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, quote)
}

func (s *Server) submitOrderHandler(c *gin.Context) {
	var request models.SubmitOrderRequest
	if !bindJSON(c, &request, "request body must be valid JSON") {
		return
	}
	request.TableNumber = strings.TrimSpace(request.TableNumber)
	if request.TableNumber == "" || utf8.RuneCountInString(request.TableNumber) > 30 {
		writeError(c, http.StatusUnprocessableEntity, "invalid_table_number", "table_number is required and cannot exceed 30 characters", nil)
		return
	}
	if request.GuestCount < 1 || request.GuestCount > 100 {
		writeError(c, http.StatusUnprocessableEntity, "invalid_guest_count", "guest_count must be between 1 and 100", nil)
		return
	}

	current, err := s.db.Menu(c.Request.Context(), true)
	if err != nil {
		writeFailure(c, http.StatusInternalServerError, "menu_unavailable", "the live menu could not be loaded", err)
		return
	}
	quote, err := menu.BuildQuote(current, models.QuoteRequest{Items: request.Items})
	if err != nil {
		var validationErrors models.ValidationErrors
		if errors.As(err, &validationErrors) {
			writeError(c, http.StatusUnprocessableEntity, "invalid_order", err.Error(), validationErrors)
			return
		}
		writeFailure(c, http.StatusInternalServerError, "order_failed", "the order could not be validated", err)
		return
	}
	order, err := s.db.CreateOrder(c.Request.Context(), request, quote)
	if err != nil {
		writeFailure(c, http.StatusInternalServerError, "order_failed", "the order could not be submitted", err)
		return
	}
	c.JSON(http.StatusCreated, order)
}

func (s *Server) adminOrdersHandler(c *gin.Context) {
	status := c.Query("status")
	if status != "" && status != models.OrderStatusNew &&
		status != models.OrderStatusSold && status != models.OrderStatusCancelled {
		writeError(c, http.StatusBadRequest, "invalid_status", "status must be new, sold, or cancelled", nil)
		return
	}
	orders, err := s.db.Orders(c.Request.Context(), status)
	if err != nil {
		writeFailure(c, http.StatusInternalServerError, "orders_unavailable", "orders could not be loaded", err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{"orders": orders})
}

func (s *Server) updateOrderStatusHandler(c *gin.Context) {
	var request struct {
		Status  string `json:"status"`
		StaffID int64  `json:"staff_id"`
	}
	if !bindJSON(c, &request, "request body must be valid JSON") {
		return
	}
	if request.Status != models.OrderStatusSold && request.Status != models.OrderStatusCancelled {
		writeError(c, http.StatusUnprocessableEntity, "invalid_status", "status must be sold or cancelled", nil)
		return
	}
	if authenticatedStaffID, exists := c.Get("staff_id"); exists {
		staffID, valid := authenticatedStaffID.(int64)
		if !valid {
			writeFailure(c, http.StatusInternalServerError, "invalid_auth_context", "the authenticated staff identity is invalid", errors.New("staff_id context value is not an int64"))
			return
		}
		request.StaffID = staffID
	}
	if request.StaffID < 1 {
		writeError(c, http.StatusUnprocessableEntity, "invalid_staff_id", "staff_id must identify the worker handling the order", nil)
		return
	}
	orderID, ok := positivePathID(c, "order")
	if !ok {
		return
	}
	order, err := s.db.UpdateOrderStatus(c.Request.Context(), orderID, request.Status, request.StaffID)
	if err != nil {
		switch {
		case errors.Is(err, database.ErrNotFound):
			writeError(c, http.StatusNotFound, "order_not_found", "order was not found", nil)
		case errors.Is(err, database.ErrInsufficientStock):
			writeError(c, http.StatusConflict, "insufficient_stock", "inventory is too low to mark this order sold", nil)
		case errors.Is(err, database.ErrInvalidTransition):
			writeError(c, http.StatusConflict, "invalid_status_transition", "this order can no longer change to that status", nil)
		case errors.Is(err, database.ErrInactiveStaff):
			writeError(c, http.StatusUnprocessableEntity, "invalid_staff", "staff member does not exist or is inactive", nil)
		default:
			writeFailure(c, http.StatusInternalServerError, "status_update_failed", "order status could not be updated", err)
		}
		return
	}
	c.JSON(http.StatusOK, order)
}

func (s *Server) menuQRHandler(c *gin.Context) {
	link, err := s.currentMenuQR(c.Request.Context())
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			writeError(c, http.StatusNotFound, "menu_qr_not_configured", "the menu QR code has not been provisioned", nil)
			return
		}
		writeFailure(c, http.StatusInternalServerError, "menu_qr_unavailable", "the menu QR code could not be loaded", err)
		return
	}
	c.JSON(http.StatusOK, link)
}

func (s *Server) menuQRImageHandler(c *gin.Context) {
	link, err := s.currentMenuQR(c.Request.Context())
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			writeError(c, http.StatusNotFound, "menu_qr_not_configured", "the menu QR code has not been provisioned", nil)
			return
		}
		writeFailure(c, http.StatusInternalServerError, "menu_qr_unavailable", "the menu QR code could not be loaded", err)
		return
	}
	image, err := s.dub.QRCode(c.Request.Context(), link.QRCode)
	if err != nil {
		writeFailure(c, http.StatusBadGateway, "dub_unavailable", "the QR image could not be loaded from Dub", err)
		return
	}
	c.Header("Cache-Control", "public, max-age=86400")
	c.Data(http.StatusOK, "image/png", image)
}

func (s *Server) provisionMenuQRHandler(c *gin.Context) {
	if s.config.DubAPIKey == "" {
		writeError(c, http.StatusServiceUnavailable, "dub_not_configured", "DUB_API_KEY is not configured", nil)
		return
	}
	if s.config.DubDomain == "" || strings.Contains(s.config.DubDomain, "://") || strings.Contains(s.config.DubDomain, "/") {
		writeError(c, http.StatusServiceUnavailable, "dub_not_configured", "DUB_DOMAIN must be a hostname such as dub.sh", nil)
		return
	}
	if s.config.DubLinkKey == "" {
		writeError(c, http.StatusServiceUnavailable, "dub_not_configured", "DUB_LINK_KEY is not configured", nil)
		return
	}
	if s.config.MenuAppURL == "" {
		writeError(c, http.StatusServiceUnavailable, "menu_app_not_configured", "MENU_APP_URL is not configured", nil)
		return
	}
	if existing, err := s.db.MenuQR(c.Request.Context()); err == nil {
		current, retrieveErr := s.dub.RetrieveMenuLink(c.Request.Context(), existing.ID, s.config.DubDomain, s.config.DubLinkKey)
		if retrieveErr != nil {
			writeFailure(c, http.StatusBadGateway, "dub_link_failed", "Dub could not retrieve the existing menu link", retrieveErr)
			return
		}
		if err := s.db.SaveMenuQR(c.Request.Context(), current); err != nil {
			writeFailure(c, http.StatusInternalServerError, "menu_qr_save_failed", "the current Dub link could not be saved", err)
			return
		}
		c.JSON(http.StatusOK, current)
		return
	} else if !errors.Is(err, database.ErrNotFound) {
		writeFailure(c, http.StatusInternalServerError, "menu_qr_unavailable", "the menu QR configuration could not be loaded", err)
		return
	}
	if current, err := s.dub.RetrieveMenuLink(c.Request.Context(), "", s.config.DubDomain, s.config.DubLinkKey); err == nil {
		if err := s.db.SaveMenuQR(c.Request.Context(), current); err != nil {
			writeFailure(c, http.StatusInternalServerError, "menu_qr_save_failed", "the existing Dub link could not be saved", err)
			return
		}
		c.JSON(http.StatusOK, current)
		return
	} else if !errors.Is(err, dub.ErrNotFound) {
		writeFailure(c, http.StatusBadGateway, "dub_link_failed", "Dub could not retrieve the menu link", err)
		return
	}

	link, err := s.dub.CreateMenuLink(
		c.Request.Context(), s.config.MenuAppURL, s.config.DubDomain, s.config.DubLinkKey,
	)
	if err != nil {
		writeFailure(c, http.StatusBadGateway, "dub_link_failed", "Dub could not create the menu link", err)
		return
	}
	if err := s.db.SaveMenuQR(c.Request.Context(), link); err != nil {
		writeFailure(c, http.StatusInternalServerError, "menu_qr_save_failed", "the Dub link was created but could not be saved", err)
		return
	}
	c.JSON(http.StatusCreated, link)
}

func (s *Server) currentMenuQR(ctx context.Context) (models.Link, error) {
	stored, storedErr := s.db.MenuQR(ctx)
	if s.config.DubAPIKey == "" {
		return stored, storedErr
	}

	linkID := ""
	if storedErr == nil {
		linkID = stored.ID
	} else if !errors.Is(storedErr, database.ErrNotFound) {
		return models.Link{}, storedErr
	}
	current, err := s.dub.RetrieveMenuLink(ctx, linkID, s.config.DubDomain, s.config.DubLinkKey)
	if err != nil {
		if storedErr == nil {
			return stored, nil
		}
		if errors.Is(err, dub.ErrNotFound) {
			return models.Link{}, database.ErrNotFound
		}
		return models.Link{}, err
	}
	if err := s.db.SaveMenuQR(ctx, current); err != nil {
		return models.Link{}, err
	}
	return current, nil
}

func (s *Server) updateInventoryHandler(c *gin.Context) {
	var request struct {
		StockQuantity *int `json:"stock_qty"`
	}
	if !bindJSON(c, &request, "request body must be valid JSON") {
		return
	}
	if request.StockQuantity == nil {
		writeError(c, http.StatusBadRequest, "invalid_request", "stock_qty is required", nil)
		return
	}
	if *request.StockQuantity < 0 {
		writeError(c, http.StatusUnprocessableEntity, "invalid_stock_qty", "stock_qty cannot be negative", nil)
		return
	}
	if *request.StockQuantity > math.MaxInt32 {
		writeError(c, http.StatusUnprocessableEntity, "invalid_stock_qty", "stock_qty cannot exceed 2147483647", nil)
		return
	}
	itemID, ok := positivePathID(c, "item")
	if !ok {
		return
	}
	inventory, err := s.db.UpdateInventory(c.Request.Context(), itemID, *request.StockQuantity)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			writeError(c, http.StatusNotFound, "item_not_found", "menu item was not found", nil)
			return
		}
		if errors.Is(err, database.ErrStockNotTracked) {
			writeError(c, http.StatusConflict, "stock_not_tracked", "this menu item does not track stock", nil)
			return
		}
		writeFailure(c, http.StatusInternalServerError, "inventory_update_failed", "inventory could not be updated", err)
		return
	}
	c.JSON(http.StatusOK, inventory)
}

func (s *Server) updateVariantInventoryHandler(c *gin.Context) {
	var request struct {
		StockQuantity *int `json:"stock_qty"`
	}
	if !bindJSON(c, &request, "request body must be valid JSON") {
		return
	}
	if request.StockQuantity == nil {
		writeError(c, http.StatusBadRequest, "invalid_request", "stock_qty is required", nil)
		return
	}
	if *request.StockQuantity < 0 {
		writeError(c, http.StatusUnprocessableEntity, "invalid_stock_qty", "stock_qty cannot be negative", nil)
		return
	}
	if *request.StockQuantity > math.MaxInt32 {
		writeError(c, http.StatusUnprocessableEntity, "invalid_stock_qty", "stock_qty cannot exceed 2147483647", nil)
		return
	}
	variantID, ok := positivePathID(c, "variant")
	if !ok {
		return
	}
	inventory, err := s.db.UpdateVariantInventory(c.Request.Context(), variantID, *request.StockQuantity)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			writeError(c, http.StatusNotFound, "variant_not_found", "variant option was not found", nil)
			return
		}
		if errors.Is(err, database.ErrStockNotTracked) {
			writeError(c, http.StatusConflict, "stock_not_tracked", "this variant does not track stock", nil)
			return
		}
		writeFailure(c, http.StatusInternalServerError, "inventory_update_failed", "variant inventory could not be updated", err)
		return
	}
	c.JSON(http.StatusOK, inventory)
}

func (s *Server) updateItemAvailabilityHandler(c *gin.Context) {
	var request struct {
		Available *bool `json:"available"`
	}
	if !bindJSON(c, &request, "request body must be valid JSON") {
		return
	}
	if request.Available == nil {
		writeError(c, http.StatusBadRequest, "invalid_request", "available is required", nil)
		return
	}
	itemID, ok := positivePathID(c, "item")
	if !ok {
		return
	}
	availability, err := s.db.UpdateItemAvailability(c.Request.Context(), itemID, *request.Available)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			writeError(c, http.StatusNotFound, "item_not_found", "menu item was not found", nil)
			return
		}
		writeFailure(c, http.StatusInternalServerError, "availability_update_failed", "item availability could not be updated", err)
		return
	}
	c.JSON(http.StatusOK, availability)
}

func (s *Server) updateItemImageHandler(c *gin.Context) {
	var request struct {
		ImageURL *string `json:"image_url"`
	}
	if !bindJSON(c, &request, "request body must be valid JSON") {
		return
	}
	if request.ImageURL == nil {
		writeError(c, http.StatusBadRequest, "invalid_request", "image_url is required", nil)
		return
	}

	imageURL := strings.TrimSpace(*request.ImageURL)
	if utf8.RuneCountInString(imageURL) > 2048 || !menu.ValidImageURL(imageURL) {
		writeError(c, http.StatusUnprocessableEntity, "invalid_image_url", "image_url must be empty, an absolute HTTP(S) URL, or a root-relative path", nil)
		return
	}

	itemID, ok := positivePathID(c, "item")
	if !ok {
		return
	}
	image, err := s.db.UpdateItemImage(c.Request.Context(), itemID, imageURL)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			writeError(c, http.StatusNotFound, "item_not_found", "menu item was not found", nil)
			return
		}
		writeFailure(c, http.StatusInternalServerError, "image_update_failed", "menu item image could not be updated", err)
		return
	}
	c.JSON(http.StatusOK, image)
}

func (s *Server) createMenuItemsHandler(c *gin.Context) {
	var request []models.NewItem
	if !bindJSON(c, &request, "request body must be a JSON array of menu items") {
		return
	}
	items, err := menu.NormalizeAndValidateNewItems(request)
	if err != nil {
		var validationErrors models.ValidationErrors
		if errors.As(err, &validationErrors) {
			writeError(c, http.StatusUnprocessableEntity, "invalid_menu_items", err.Error(), validationErrors)
			return
		}
		writeFailure(c, http.StatusInternalServerError, "menu_item_validation_failed", "menu items could not be validated", err)
		return
	}
	created, err := s.db.CreateMenuItems(c.Request.Context(), items)
	if err != nil {
		switch {
		case errors.Is(err, database.ErrConflict):
			writeError(c, http.StatusConflict, "menu_item_conflict", "an item or variant with that name already exists", nil)
		case errors.Is(err, database.ErrInvalidCategory):
			writeError(c, http.StatusUnprocessableEntity, "invalid_category", "one or more category IDs do not exist", nil)
		default:
			writeFailure(c, http.StatusInternalServerError, "menu_item_create_failed", "menu items could not be created", err)
		}
		return
	}
	c.JSON(http.StatusCreated, gin.H{"items": created})
}

func (s *Server) menuCategoriesHandler(c *gin.Context) {
	categories, err := s.db.Categories(c.Request.Context())
	if err != nil {
		writeFailure(c, http.StatusInternalServerError, "categories_unavailable", "menu categories could not be loaded", err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{"categories": categories})
}

func (s *Server) createMenuCategoriesHandler(c *gin.Context) {
	var request []models.NewCategory
	if !bindJSON(c, &request, "request body must be a JSON array of categories") {
		return
	}
	categories, err := menu.NormalizeAndValidateNewCategories(request)
	if err != nil {
		var validationErrors models.ValidationErrors
		if errors.As(err, &validationErrors) {
			writeError(c, http.StatusUnprocessableEntity, "invalid_categories", err.Error(), validationErrors)
			return
		}
		writeFailure(c, http.StatusInternalServerError, "category_validation_failed", "categories could not be validated", err)
		return
	}
	created, err := s.db.CreateCategories(c.Request.Context(), categories)
	if err != nil {
		if errors.Is(err, database.ErrConflict) {
			writeError(c, http.StatusConflict, "category_conflict", "one or more category names already exist", nil)
			return
		}
		writeFailure(c, http.StatusInternalServerError, "category_create_failed", "categories could not be created", err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"categories": created})
}

func (s *Server) staffHandler(c *gin.Context) {
	staff, err := s.db.Staff(c.Request.Context(), c.Query("include_inactive") == "true")
	if err != nil {
		writeFailure(c, http.StatusInternalServerError, "staff_unavailable", "staff could not be loaded", err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{"staff": staff})
}

func (s *Server) createStaffHandler(c *gin.Context) {
	var request struct {
		Name string `json:"name"`
		PIN  string `json:"pin"`
	}
	if !bindJSON(c, &request, "request body must be valid JSON") {
		return
	}
	request.Name = strings.TrimSpace(request.Name)
	request.PIN = strings.TrimSpace(request.PIN)
	if request.Name == "" || utf8.RuneCountInString(request.Name) > 80 {
		writeError(c, http.StatusUnprocessableEntity, "invalid_staff_name", "name is required and cannot exceed 80 characters", nil)
		return
	}
	pinLength := utf8.RuneCountInString(request.PIN)
	if pinLength < 4 || pinLength > 32 {
		writeError(c, http.StatusUnprocessableEntity, "invalid_pin", "pin must contain between 4 and 32 characters", nil)
		return
	}
	pinHash, err := hashPIN(request.PIN)
	if err != nil {
		writeFailure(c, http.StatusInternalServerError, "staff_create_failed", "staff member could not be created", err)
		return
	}
	member, err := s.db.CreateStaff(c.Request.Context(), request.Name, pinHash)
	if err != nil {
		if errors.Is(err, database.ErrConflict) {
			writeError(c, http.StatusConflict, "staff_conflict", "a staff member with that name already exists", nil)
			return
		}
		writeFailure(c, http.StatusInternalServerError, "staff_create_failed", "staff member could not be created", err)
		return
	}
	c.JSON(http.StatusCreated, member)
}

func hashPIN(pin string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	const iterations = 210_000
	derived, err := pbkdf2.Key(sha256.New, pin, salt, iterations, 32)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("pbkdf2_sha256$%d$%s$%s", iterations,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(derived)), nil
}

func verifyPIN(pin, encoded string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2_sha256" {
		return false
	}
	iterations, err := strconv.Atoi(parts[1])
	if err != nil || iterations < 10_000 || iterations > 1_000_000 {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[2])
	if err != nil || len(salt) < 8 {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil || len(want) != 32 {
		return false
	}
	derived, err := pbkdf2.Key(sha256.New, pin, salt, iterations, len(want))
	return err == nil && subtle.ConstantTimeCompare(derived, want) == 1
}

func (s *Server) staffLoginHandler(c *gin.Context) {
	if s.config.AdminAPIKey == "" {
		writeError(c, http.StatusServiceUnavailable, "staff_login_not_configured", "ADMIN_API_KEY is required to sign staff sessions", nil)
		return
	}
	var request struct {
		Name string `json:"name"`
		PIN  string `json:"pin"`
	}
	if !bindJSON(c, &request, "request body must be valid JSON") {
		return
	}
	request.Name = strings.TrimSpace(request.Name)
	request.PIN = strings.TrimSpace(request.PIN)
	pinLength := utf8.RuneCountInString(request.PIN)
	if request.Name == "" || utf8.RuneCountInString(request.Name) > 80 || pinLength < 4 || pinLength > 32 {
		writeError(c, http.StatusUnauthorized, "invalid_staff_login", "staff name or PIN is invalid", nil)
		return
	}
	member, pinHash, err := s.db.StaffCredentials(c.Request.Context(), request.Name)
	if err != nil && !errors.Is(err, database.ErrNotFound) {
		reportError(c, err)
	}
	if err != nil {
		pinHash = dummyPINHash
	}
	validPIN := verifyPIN(request.PIN, pinHash)
	if err != nil || !member.Active || !validPIN {
		writeError(c, http.StatusUnauthorized, "invalid_staff_login", "staff name or PIN is invalid", nil)
		return
	}
	expiresAt := time.Now().UTC().Add(12 * time.Hour)
	c.JSON(http.StatusOK, gin.H{
		"staff": member, "access_token": s.signStaffToken(member.ID, expiresAt), "expires_at": expiresAt,
	})
}

func (s *Server) signStaffToken(staffID int64, expiresAt time.Time) string {
	payload := base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf("%d:%d", staffID, expiresAt.Unix())))
	mac := hmac.New(sha256.New, []byte(s.config.AdminAPIKey))
	_, _ = mac.Write([]byte(payload))
	return payload + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (s *Server) verifyStaffToken(token string) (int64, bool) {
	payloadPart, signaturePart, found := strings.Cut(token, ".")
	if !found || payloadPart == "" || signaturePart == "" || strings.Contains(signaturePart, ".") {
		return 0, false
	}
	providedSignature, err := base64.RawURLEncoding.DecodeString(signaturePart)
	if err != nil {
		return 0, false
	}
	mac := hmac.New(sha256.New, []byte(s.config.AdminAPIKey))
	_, _ = mac.Write([]byte(payloadPart))
	if !hmac.Equal(providedSignature, mac.Sum(nil)) {
		return 0, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(payloadPart)
	if err != nil {
		return 0, false
	}
	staffIDValue, expiresValue, found := strings.Cut(string(payload), ":")
	if !found || strings.Contains(expiresValue, ":") {
		return 0, false
	}
	staffID, idErr := strconv.ParseInt(staffIDValue, 10, 64)
	expiresUnix, expiryErr := strconv.ParseInt(expiresValue, 10, 64)
	if idErr != nil || expiryErr != nil || staffID < 1 || time.Now().Unix() >= expiresUnix {
		return 0, false
	}
	return staffID, true
}

func (s *Server) updateStaffActiveHandler(c *gin.Context) {
	var request struct {
		Active *bool `json:"active"`
	}
	if !bindJSON(c, &request, "request body must be valid JSON") {
		return
	}
	if request.Active == nil {
		writeError(c, http.StatusBadRequest, "invalid_request", "active is required", nil)
		return
	}
	staffID, ok := positivePathID(c, "staff")
	if !ok {
		return
	}
	member, err := s.db.SetStaffActive(c.Request.Context(), staffID, *request.Active)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			writeError(c, http.StatusNotFound, "staff_not_found", "staff member was not found", nil)
			return
		}
		writeFailure(c, http.StatusInternalServerError, "staff_update_failed", "staff member could not be updated", err)
		return
	}
	c.JSON(http.StatusOK, member)
}

func positivePathID(c *gin.Context, resource string) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id < 1 {
		writeError(c, http.StatusBadRequest, "invalid_"+resource+"_id", resource+" ID must be a positive integer", nil)
		return 0, false
	}
	return id, true
}

func (s *Server) requireAdminKey() gin.HandlerFunc {
	return func(c *gin.Context) {
		if s.config.AdminAPIKey == "" {
			writeError(c, http.StatusServiceUnavailable, "admin_not_configured", "ADMIN_API_KEY is not configured", nil)
			c.Abort()
			return
		}
		provided := c.GetHeader("X-Admin-Key")
		valid := len(provided) == len(s.config.AdminAPIKey) &&
			subtle.ConstantTimeCompare([]byte(provided), []byte(s.config.AdminAPIKey)) == 1
		if !valid {
			const bearerPrefix = "Bearer "
			if authorization := c.GetHeader("Authorization"); strings.HasPrefix(authorization, bearerPrefix) {
				if staffID, tokenValid := s.verifyStaffToken(strings.TrimSpace(strings.TrimPrefix(authorization, bearerPrefix))); tokenValid {
					active, err := s.db.StaffActive(c.Request.Context(), staffID)
					if err != nil && !errors.Is(err, database.ErrNotFound) {
						reportError(c, err)
					}
					if err == nil && active {
						valid = true
						c.Set("staff_id", staffID)
					}
				}
			}
		}
		if !valid {
			writeError(c, http.StatusUnauthorized, "unauthorized", "a valid X-Admin-Key or staff bearer token is required", nil)
			c.Abort()
			return
		}
		c.Next()
	}
}

func writeError(c *gin.Context, status int, code, message string, details any) {
	errorBody := gin.H{"code": code, "message": message}
	if details != nil {
		errorBody["details"] = details
	}
	c.JSON(status, gin.H{"error": errorBody})
}

func writeFailure(c *gin.Context, status int, code, message string, cause error) {
	reportError(c, cause)
	writeError(c, status, code, message, nil)
}

func bindJSON(c *gin.Context, destination any, invalidMessage string) bool {
	if err := c.ShouldBindJSON(destination); err != nil {
		var sizeError *http.MaxBytesError
		if errors.As(err, &sizeError) {
			writeError(c, http.StatusRequestEntityTooLarge, "request_too_large", "request body may not exceed 1 MiB", nil)
			return false
		}
		writeError(c, http.StatusBadRequest, "invalid_request", invalidMessage, nil)
		return false
	}
	return true
}
