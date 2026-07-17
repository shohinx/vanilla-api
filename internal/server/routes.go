package server

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"

	"github.com/shohinx/vanilla-api/internal/database"
	"github.com/shohinx/vanilla-api/internal/dub"
	"github.com/shohinx/vanilla-api/internal/imageopt"
	"github.com/shohinx/vanilla-api/internal/imagestore"
	"github.com/shohinx/vanilla-api/internal/menu"
)

const maxImageBytes int64 = 50 << 20

func (s *Server) RegisterRoutes() http.Handler {
	router := gin.New()
	router.Use(gin.Logger(), gin.Recovery())
	router.Use(cors.New(cors.Config{
		AllowOrigins:     s.config.AllowedOrigins,
		AllowMethods:     []string{"GET", "POST", "PATCH", "OPTIONS"},
		AllowHeaders:     []string{"Accept", "Authorization", "Content-Type", "X-Admin-Key"},
		AllowCredentials: false,
	}))

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

	admin := api.Group("/admin")
	admin.Use(s.requireAdminKey())
	admin.PATCH("/items/:id/inventory", s.updateInventoryHandler)
	admin.PATCH("/items/:id/image", s.updateItemImageHandler)
	admin.GET("/menu/categories", s.menuCategoriesHandler)
	admin.POST("/menu/categories", s.createMenuCategoriesHandler)
	admin.POST("/menu/items", s.createMenuItemsHandler)
	admin.POST("/images", s.uploadImageHandler)
	admin.GET("/orders", s.adminOrdersHandler)
	admin.PATCH("/orders/:id/status", s.updateOrderStatusHandler)
	admin.POST("/menu/qr", s.provisionMenuQRHandler)

	return router
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
	defer file.Close()

	var header [512]byte
	headerSize, err := io.ReadFull(file, header[:])
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		writeError(c, http.StatusBadRequest, "invalid_image_upload", "the uploaded image could not be read", nil)
		return
	}
	contentType := http.DetectContentType(header[:headerSize])
	_, supported := imageExtension(contentType)
	if !supported {
		writeError(c, http.StatusUnsupportedMediaType, "unsupported_image_type", "only JPEG, PNG, and WebP images are supported", nil)
		return
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_image_upload", "the uploaded image could not be read", nil)
		return
	}
	optimized, err := imageopt.Optimize(file)
	if errors.Is(err, imageopt.ErrTooManyPixels) {
		writeError(c, http.StatusUnprocessableEntity, "image_dimensions_too_large", "images may not exceed 40 megapixels", nil)
		return
	}
	if errors.Is(err, imageopt.ErrInvalidImage) {
		writeError(c, http.StatusUnprocessableEntity, "invalid_image", "the uploaded file is not a valid JPEG, PNG, or WebP image", nil)
		return
	}
	if errors.Is(err, imageopt.ErrCannotCompress) {
		writeError(c, http.StatusUnprocessableEntity, "image_could_not_be_compressed", "the image could not be compressed below 2 MiB", nil)
		return
	}
	if err != nil {
		writeError(c, http.StatusInternalServerError, "image_optimization_failed", "the image could not be optimized", nil)
		return
	}

	key, err := newImageKey(".webp")
	if err != nil {
		writeError(c, http.StatusInternalServerError, "image_key_failed", "an image key could not be generated", nil)
		return
	}
	optimizedSize := int64(len(optimized.Data))
	if err := s.images.Put(c.Request.Context(), key, bytes.NewReader(optimized.Data), optimizedSize, "image/webp"); err != nil {
		writeError(c, http.StatusBadGateway, "image_upload_failed", "the image could not be stored", nil)
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
		if errors.Is(err, imagestore.ErrNotFound) {
			writeError(c, http.StatusNotFound, "image_not_found", "image was not found", nil)
			return
		}
		writeError(c, http.StatusBadGateway, "image_unavailable", "the image could not be loaded", nil)
		return
	}
	defer object.Body.Close()

	headers := map[string]string{"Cache-Control": "public, max-age=31536000, immutable"}
	if object.ETag != "" {
		headers["ETag"] = "\"" + object.ETag + "\""
	}
	c.DataFromReader(http.StatusOK, object.ContentLength, object.ContentType, object.Body, headers)
}

func imageExtension(contentType string) (string, bool) {
	switch contentType {
	case "image/jpeg":
		return ".jpg", true
	case "image/png":
		return ".png", true
	case "image/webp":
		return ".webp", true
	default:
		return "", false
	}
}

func newImageKey(extension string) (string, error) {
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return "menu/" + hex.EncodeToString(random) + extension, nil
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
	health := s.db.Health(c.Request.Context())
	status := http.StatusOK
	if health["status"] != "up" {
		status = http.StatusServiceUnavailable
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
		writeError(c, http.StatusInternalServerError, "menu_unavailable", "the live menu could not be loaded", nil)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, current)
}

func (s *Server) quoteHandler(c *gin.Context) {
	var request menu.QuoteRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", "request body must be valid JSON", nil)
		return
	}

	current, err := s.db.Menu(c.Request.Context(), true)
	if err != nil {
		writeError(c, http.StatusInternalServerError, "menu_unavailable", "the live menu could not be loaded", nil)
		return
	}
	quote, err := menu.BuildQuote(current, request)
	if err != nil {
		var validationErrors menu.ValidationErrors
		if errors.As(err, &validationErrors) {
			writeError(c, http.StatusUnprocessableEntity, "invalid_order_plan", err.Error(), validationErrors)
			return
		}
		writeError(c, http.StatusInternalServerError, "quote_failed", "the order plan could not be priced", nil)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, quote)
}

func (s *Server) submitOrderHandler(c *gin.Context) {
	var request menu.SubmitOrderRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", "request body must be valid JSON", nil)
		return
	}
	request.CustomerName = strings.TrimSpace(request.CustomerName)
	request.Notes = strings.TrimSpace(request.Notes)
	if request.CustomerName == "" || len(request.CustomerName) > 80 {
		writeError(c, http.StatusUnprocessableEntity, "invalid_customer_name", "customer_name is required and cannot exceed 80 characters", nil)
		return
	}
	if len(request.Notes) > 500 {
		writeError(c, http.StatusUnprocessableEntity, "invalid_notes", "notes cannot exceed 500 characters", nil)
		return
	}

	current, err := s.db.Menu(c.Request.Context(), true)
	if err != nil {
		writeError(c, http.StatusInternalServerError, "menu_unavailable", "the live menu could not be loaded", nil)
		return
	}
	quote, err := menu.BuildQuote(current, menu.QuoteRequest{Items: request.Items})
	if err != nil {
		var validationErrors menu.ValidationErrors
		if errors.As(err, &validationErrors) {
			writeError(c, http.StatusUnprocessableEntity, "invalid_order", err.Error(), validationErrors)
			return
		}
		writeError(c, http.StatusInternalServerError, "order_failed", "the order could not be validated", nil)
		return
	}
	order, err := s.db.CreateOrder(c.Request.Context(), request, quote)
	if err != nil {
		writeError(c, http.StatusInternalServerError, "order_failed", "the order could not be submitted", nil)
		return
	}
	c.JSON(http.StatusCreated, order)
}

func (s *Server) adminOrdersHandler(c *gin.Context) {
	status := c.Query("status")
	if status != "" && status != "submitted" && status != "sold" && status != "cancelled" {
		writeError(c, http.StatusBadRequest, "invalid_status", "status must be submitted, sold, or cancelled", nil)
		return
	}
	orders, err := s.db.Orders(c.Request.Context(), status)
	if err != nil {
		writeError(c, http.StatusInternalServerError, "orders_unavailable", "orders could not be loaded", nil)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{"orders": orders})
}

func (s *Server) updateOrderStatusHandler(c *gin.Context) {
	var request struct {
		Status string `json:"status"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", "request body must be valid JSON", nil)
		return
	}
	if request.Status != "sold" && request.Status != "cancelled" {
		writeError(c, http.StatusUnprocessableEntity, "invalid_status", "status must be sold or cancelled", nil)
		return
	}
	order, err := s.db.UpdateOrderStatus(c.Request.Context(), c.Param("id"), request.Status)
	if err != nil {
		switch {
		case errors.Is(err, database.ErrNotFound):
			writeError(c, http.StatusNotFound, "order_not_found", "order was not found", nil)
		case errors.Is(err, database.ErrInsufficientStock):
			writeError(c, http.StatusConflict, "insufficient_stock", "inventory is too low to mark this order sold", nil)
		case errors.Is(err, database.ErrInvalidTransition):
			writeError(c, http.StatusConflict, "invalid_status_transition", "this order can no longer change to that status", nil)
		default:
			writeError(c, http.StatusInternalServerError, "status_update_failed", "order status could not be updated", nil)
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
		writeError(c, http.StatusInternalServerError, "menu_qr_unavailable", "the menu QR code could not be loaded", nil)
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
		writeError(c, http.StatusInternalServerError, "menu_qr_unavailable", "the menu QR code could not be loaded", nil)
		return
	}
	image, err := s.dub.QRCode(c.Request.Context(), link.QRCode)
	if err != nil {
		writeError(c, http.StatusBadGateway, "dub_unavailable", "the QR image could not be loaded from Dub", nil)
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
	if s.config.MenuAppURL == "" {
		writeError(c, http.StatusServiceUnavailable, "menu_app_not_configured", "MENU_APP_URL is not configured", nil)
		return
	}
	if existing, err := s.db.MenuQR(c.Request.Context()); err == nil {
		current, retrieveErr := s.dub.RetrieveMenuLink(c.Request.Context(), existing.ID)
		if retrieveErr != nil {
			writeError(c, http.StatusBadGateway, "dub_link_failed", "Dub could not retrieve the existing menu link", nil)
			return
		}
		if err := s.db.SaveMenuQR(c.Request.Context(), current); err != nil {
			writeError(c, http.StatusInternalServerError, "menu_qr_save_failed", "the current Dub link could not be saved", nil)
			return
		}
		c.JSON(http.StatusOK, current)
		return
	} else if !errors.Is(err, database.ErrNotFound) {
		writeError(c, http.StatusInternalServerError, "menu_qr_unavailable", "the menu QR configuration could not be loaded", nil)
		return
	}
	if current, err := s.dub.RetrieveMenuLink(c.Request.Context(), ""); err == nil {
		if err := s.db.SaveMenuQR(c.Request.Context(), current); err != nil {
			writeError(c, http.StatusInternalServerError, "menu_qr_save_failed", "the existing Dub link could not be saved", nil)
			return
		}
		c.JSON(http.StatusOK, current)
		return
	} else if !errors.Is(err, dub.ErrNotFound) {
		writeError(c, http.StatusBadGateway, "dub_link_failed", "Dub could not retrieve the menu link", nil)
		return
	}

	link, err := s.dub.CreateMenuLink(
		c.Request.Context(), s.config.MenuAppURL, s.config.DubDomain, s.config.DubLinkKey,
	)
	if err != nil {
		writeError(c, http.StatusBadGateway, "dub_link_failed", "Dub could not create the menu link", nil)
		return
	}
	if err := s.db.SaveMenuQR(c.Request.Context(), link); err != nil {
		writeError(c, http.StatusInternalServerError, "menu_qr_save_failed", "the Dub link was created but could not be saved", nil)
		return
	}
	c.JSON(http.StatusCreated, link)
}

func (s *Server) currentMenuQR(ctx context.Context) (dub.Link, error) {
	stored, storedErr := s.db.MenuQR(ctx)
	if s.config.DubAPIKey == "" {
		return stored, storedErr
	}

	linkID := ""
	if storedErr == nil {
		linkID = stored.ID
	} else if !errors.Is(storedErr, database.ErrNotFound) {
		return dub.Link{}, storedErr
	}
	current, err := s.dub.RetrieveMenuLink(ctx, linkID)
	if err != nil {
		if storedErr == nil {
			return stored, nil
		}
		if errors.Is(err, dub.ErrNotFound) {
			return dub.Link{}, database.ErrNotFound
		}
		return dub.Link{}, err
	}
	if err := s.db.SaveMenuQR(ctx, current); err != nil {
		return dub.Link{}, err
	}
	return current, nil
}

func (s *Server) updateInventoryHandler(c *gin.Context) {
	var request struct {
		Quantity *int `json:"quantity"`
	}
	if err := c.ShouldBindJSON(&request); err != nil || request.Quantity == nil {
		writeError(c, http.StatusBadRequest, "invalid_request", "quantity is required", nil)
		return
	}
	if *request.Quantity < 0 {
		writeError(c, http.StatusUnprocessableEntity, "invalid_quantity", "quantity cannot be negative", nil)
		return
	}

	inventory, err := s.db.UpdateInventory(c.Request.Context(), c.Param("id"), *request.Quantity)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			writeError(c, http.StatusNotFound, "item_not_found", "menu item was not found", nil)
			return
		}
		writeError(c, http.StatusInternalServerError, "inventory_update_failed", "inventory could not be updated", nil)
		return
	}
	c.JSON(http.StatusOK, inventory)
}

func (s *Server) updateItemImageHandler(c *gin.Context) {
	var request struct {
		ImageURL *string `json:"image_url"`
	}
	if err := c.ShouldBindJSON(&request); err != nil || request.ImageURL == nil {
		writeError(c, http.StatusBadRequest, "invalid_request", "image_url is required", nil)
		return
	}

	imageURL := strings.TrimSpace(*request.ImageURL)
	if len(imageURL) > 2048 || !validMenuImageURL(imageURL) {
		writeError(c, http.StatusUnprocessableEntity, "invalid_image_url", "image_url must be empty, an absolute HTTP(S) URL, or a root-relative path", nil)
		return
	}

	image, err := s.db.UpdateItemImage(c.Request.Context(), c.Param("id"), imageURL)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			writeError(c, http.StatusNotFound, "item_not_found", "menu item was not found", nil)
			return
		}
		writeError(c, http.StatusInternalServerError, "image_update_failed", "menu item image could not be updated", nil)
		return
	}
	c.JSON(http.StatusOK, image)
}

func validMenuImageURL(value string) bool {
	if value == "" {
		return true
	}
	parsed, err := url.ParseRequestURI(value)
	if err != nil {
		return false
	}
	if parsed.IsAbs() {
		return (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != ""
	}
	return strings.HasPrefix(parsed.Path, "/") && !strings.HasPrefix(value, "//")
}

func (s *Server) createMenuItemsHandler(c *gin.Context) {
	var request []menu.NewItem
	if err := c.ShouldBindJSON(&request); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", "request body must be a JSON array of menu items", nil)
		return
	}
	items, err := menu.NormalizeAndValidateNewItems(request)
	if err != nil {
		var validationErrors menu.ValidationErrors
		if errors.As(err, &validationErrors) {
			writeError(c, http.StatusUnprocessableEntity, "invalid_menu_items", err.Error(), validationErrors)
			return
		}
		writeError(c, http.StatusInternalServerError, "menu_item_validation_failed", "menu items could not be validated", nil)
		return
	}
	created, err := s.db.CreateMenuItems(c.Request.Context(), items)
	if err != nil {
		switch {
		case errors.Is(err, database.ErrConflict):
			writeError(c, http.StatusConflict, "menu_item_conflict", "an item, modifier group, or option ID already exists", nil)
		case errors.Is(err, database.ErrInvalidCategory):
			writeError(c, http.StatusUnprocessableEntity, "invalid_category", "one or more category IDs do not exist", nil)
		default:
			writeError(c, http.StatusInternalServerError, "menu_item_create_failed", "menu items could not be created", nil)
		}
		return
	}
	c.JSON(http.StatusCreated, gin.H{"items": created})
}

func (s *Server) menuCategoriesHandler(c *gin.Context) {
	categories, err := s.db.Categories(c.Request.Context())
	if err != nil {
		writeError(c, http.StatusInternalServerError, "categories_unavailable", "menu categories could not be loaded", nil)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{"categories": categories})
}

func (s *Server) createMenuCategoriesHandler(c *gin.Context) {
	var request []menu.NewCategory
	if err := c.ShouldBindJSON(&request); err != nil {
		writeError(c, http.StatusBadRequest, "invalid_request", "request body must be a JSON array of categories", nil)
		return
	}
	categories, err := menu.NormalizeAndValidateNewCategories(request)
	if err != nil {
		var validationErrors menu.ValidationErrors
		if errors.As(err, &validationErrors) {
			writeError(c, http.StatusUnprocessableEntity, "invalid_categories", err.Error(), validationErrors)
			return
		}
		writeError(c, http.StatusInternalServerError, "category_validation_failed", "categories could not be validated", nil)
		return
	}
	created, err := s.db.CreateCategories(c.Request.Context(), categories)
	if err != nil {
		if errors.Is(err, database.ErrConflict) {
			writeError(c, http.StatusConflict, "category_conflict", "one or more category IDs already exist", nil)
			return
		}
		writeError(c, http.StatusInternalServerError, "category_create_failed", "categories could not be created", nil)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"categories": created})
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
			writeError(c, http.StatusUnauthorized, "unauthorized", "a valid X-Admin-Key header is required", nil)
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
