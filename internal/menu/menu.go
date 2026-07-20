package menu

import (
	"fmt"
	"math"
	"net/url"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/shohinx/vanilla-api/internal/sdk/models"
)

const (
	maxItemsPerRequest      = 100
	maxCategoriesPerRequest = 50
	maxItemNameLength       = 120
	maxCategoryNameLength   = 80
	maxVariantNameLength    = 80
	maxDescriptionLength    = 1_000
	maxImageURLLength       = 2_048
	maxDatabaseInteger      = math.MaxInt32
)

// NormalizeAndValidateNewItems returns a normalized copy of items. The input
// remains untouched, which lets callers safely retain or retry request values.
func NormalizeAndValidateNewItems(items []models.NewItem) ([]models.NewItem, error) {
	if len(items) == 0 {
		return nil, models.ValidationErrors{{Field: "items", Message: "must contain at least one item"}}
	}
	if len(items) > maxItemsPerRequest {
		return nil, models.ValidationErrors{{Field: "items", Message: "cannot contain more than 100 items"}}
	}

	normalized := cloneNewItems(items)
	seenItems := make(map[string]struct{}, len(normalized))
	var validationErrors models.ValidationErrors

	for itemIndex := range normalized {
		item := &normalized[itemIndex]
		field := fmt.Sprintf("items[%d]", itemIndex)
		normalizeNewItem(item)

		validateItem(item, field, &validationErrors)
		itemKey := fmt.Sprintf("%d:%s", item.CategoryID, strings.ToLower(item.Name))
		if _, exists := seenItems[itemKey]; exists && item.Name != "" {
			validationErrors = append(validationErrors, models.ValidationError{
				Field: field + ".name", Message: "must be unique within its category and request",
			})
		}
		seenItems[itemKey] = struct{}{}

		if item.VariantGroup != nil {
			validateVariantGroup(item.VariantGroup, field+".variant_group", &validationErrors)
		}
	}

	if len(validationErrors) != 0 {
		return nil, validationErrors
	}
	return normalized, nil
}

func cloneNewItems(items []models.NewItem) []models.NewItem {
	cloned := slices.Clone(items)
	for index := range cloned {
		item := &cloned[index]
		item.ImageURL = clonePointer(item.ImageURL)
		item.StockQuantity = clonePointer(item.StockQuantity)
		item.Available = clonePointer(item.Available)
		if item.VariantGroup == nil {
			continue
		}

		group := *item.VariantGroup
		group.Required = clonePointer(group.Required)
		group.Options = slices.Clone(group.Options)
		for optionIndex := range group.Options {
			option := &group.Options[optionIndex]
			option.StockQuantity = clonePointer(option.StockQuantity)
		}
		item.VariantGroup = &group
	}
	return cloned
}

func clonePointer[T any](value *T) *T {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func normalizeNewItem(item *models.NewItem) {
	item.Name = strings.TrimSpace(item.Name)
	item.Description = strings.TrimSpace(item.Description)
	if item.ImageURL != nil {
		*item.ImageURL = strings.TrimSpace(*item.ImageURL)
	}
	if item.VariantGroup == nil {
		return
	}

	group := item.VariantGroup
	group.Name = strings.TrimSpace(group.Name)
	if group.Required == nil {
		group.Required = new(true)
	}
	for index := range group.Options {
		group.Options[index].Name = strings.TrimSpace(group.Options[index].Name)
	}
}

func validateItem(item *models.NewItem, field string, validationErrors *models.ValidationErrors) {
	if item.CategoryID < 1 {
		addValidationError(validationErrors, field+".category_id", "must be a positive category ID")
	}
	if emptyOrTooLong(item.Name, maxItemNameLength) {
		addValidationError(validationErrors, field+".name", "is required and cannot exceed 120 characters")
	}
	if utf8.RuneCountInString(item.Description) > maxDescriptionLength {
		addValidationError(validationErrors, field+".description", "cannot exceed 1000 characters")
	}
	if item.PriceCents < 0 || item.PriceCents > maxDatabaseInteger {
		addValidationError(validationErrors, field+".price_cents", "must be between 0 and 2147483647")
	}
	if item.SortOrder < math.MinInt32 || item.SortOrder > maxDatabaseInteger {
		addValidationError(validationErrors, field+".sort_order", "must fit in a 32-bit integer")
	}
	if item.ImageURL != nil && (utf8.RuneCountInString(*item.ImageURL) > maxImageURLLength || !ValidImageURL(*item.ImageURL)) {
		addValidationError(validationErrors, field+".image_url", "must be empty, an absolute HTTP(S) URL, or a root-relative path")
	}
	validateStock(field, item.TrackStock, item.StockQuantity, validationErrors)
}

func validateVariantGroup(group *models.NewVariantGroup, field string, validationErrors *models.ValidationErrors) {
	if emptyOrTooLong(group.Name, maxVariantNameLength) {
		addValidationError(validationErrors, field+".name", "is required and cannot exceed 80 characters")
	}
	if !*group.Required {
		addValidationError(validationErrors, field+".required", "must be true; optional add-ons are not part of this menu model")
	}
	if len(group.Options) == 0 {
		addValidationError(validationErrors, field+".options", "must contain at least one option")
	}

	seenOptions := make(map[string]struct{}, len(group.Options))
	for optionIndex := range group.Options {
		option := &group.Options[optionIndex]
		optionField := fmt.Sprintf("%s.options[%d]", field, optionIndex)
		if emptyOrTooLong(option.Name, maxVariantNameLength) {
			addValidationError(validationErrors, optionField+".name", "is required and cannot exceed 80 characters")
		}
		key := strings.ToLower(option.Name)
		if _, exists := seenOptions[key]; exists && option.Name != "" {
			addValidationError(validationErrors, optionField+".name", "must be unique within the variant group")
		}
		seenOptions[key] = struct{}{}
		if option.PriceCents < 0 || option.PriceCents > maxDatabaseInteger {
			addValidationError(validationErrors, optionField+".price_cents", "must be between 0 and 2147483647")
		}
		validateStock(optionField, option.TrackStock, option.StockQuantity, validationErrors)
	}
}

func validateStock(field string, trackStock bool, stockQuantity *int, validationErrors *models.ValidationErrors) {
	switch {
	case trackStock && stockQuantity == nil:
		addValidationError(validationErrors, field+".stock_qty", "is required when track_stock is true")
	case !trackStock && stockQuantity != nil:
		addValidationError(validationErrors, field+".stock_qty", "must be omitted when track_stock is false")
	case stockQuantity != nil && *stockQuantity < 0:
		addValidationError(validationErrors, field+".stock_qty", "cannot be negative")
	case stockQuantity != nil && *stockQuantity > maxDatabaseInteger:
		addValidationError(validationErrors, field+".stock_qty", "cannot exceed 2147483647")
	}
}

func emptyOrTooLong(value string, maximum int) bool {
	return value == "" || utf8.RuneCountInString(value) > maximum
}

func addValidationError(validationErrors *models.ValidationErrors, field, message string) {
	*validationErrors = append(*validationErrors, models.ValidationError{Field: field, Message: message})
}

func ValidImageURL(value string) bool {
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

// NormalizeAndValidateNewCategories returns a normalized copy of categories.
func NormalizeAndValidateNewCategories(categories []models.NewCategory) ([]models.NewCategory, error) {
	if len(categories) == 0 {
		return nil, models.ValidationErrors{{Field: "categories", Message: "must contain at least one category"}}
	}
	if len(categories) > maxCategoriesPerRequest {
		return nil, models.ValidationErrors{{Field: "categories", Message: "cannot contain more than 50 categories"}}
	}

	normalized := slices.Clone(categories)
	seen := make(map[string]struct{}, len(normalized))
	var validationErrors models.ValidationErrors
	for index := range normalized {
		category := &normalized[index]
		field := fmt.Sprintf("categories[%d]", index)
		category.Name = strings.TrimSpace(category.Name)
		key := strings.ToLower(category.Name)

		if emptyOrTooLong(category.Name, maxCategoryNameLength) {
			addValidationError(&validationErrors, field+".name", "is required and cannot exceed 80 characters")
		} else if _, exists := seen[key]; exists {
			addValidationError(&validationErrors, field+".name", "must be unique within the request")
		}
		seen[key] = struct{}{}
		if category.SortOrder < math.MinInt32 || category.SortOrder > maxDatabaseInteger {
			addValidationError(&validationErrors, field+".sort_order", "must fit in a 32-bit integer")
		}
	}
	if len(validationErrors) != 0 {
		return nil, validationErrors
	}
	return normalized, nil
}

func BuildQuote(current models.Menu, request models.QuoteRequest) (models.Quote, error) {
	if len(request.Items) == 0 {
		return models.Quote{}, models.ValidationErrors{{Field: "items", Message: "must contain at least one item"}}
	}

	items := make(map[int64]models.Item)
	for item := range current.Items() {
		items[item.ID] = item
	}

	itemTotals := make(map[int64]int)
	variantTotals := make(map[int64]int)
	quote := models.Quote{Items: make([]models.QuoteLineItem, 0, len(request.Items))}
	var validationErrors models.ValidationErrors

	for index, requested := range request.Items {
		field := fmt.Sprintf("items[%d]", index)
		item, exists := items[requested.ItemID]
		if !exists {
			addValidationError(&validationErrors, field+".item_id", "item does not exist")
			continue
		}
		if requested.Quantity < 1 {
			addValidationError(&validationErrors, field+".quantity", "must be at least 1")
			continue
		}
		if !item.Available {
			addValidationError(&validationErrors, field+".item_id", "item is not available")
			continue
		}

		line, selectedOption, validationError := quoteLine(item, requested, field)
		if validationError != nil {
			validationErrors = append(validationErrors, *validationError)
			continue
		}
		if line.UnitPriceCents > math.MaxInt/requested.Quantity {
			addValidationError(&validationErrors, field+".quantity", "quantity produces a total that is too large")
			continue
		}

		line.LineTotalCents = line.UnitPriceCents * line.Quantity
		if quote.TotalCents > math.MaxInt-line.LineTotalCents {
			addValidationError(&validationErrors, field+".quantity", "order total is too large")
			continue
		}
		if !stockAvailable(item, selectedOption, requested.Quantity, itemTotals, variantTotals) {
			message := "requested quantity is not available"
			if selectedOption != nil && selectedOption.TrackStock {
				message = "requested variant quantity is not available"
			}
			addValidationError(&validationErrors, field+".quantity", message)
			continue
		}
		quote.TotalCents += line.LineTotalCents
		quote.Items = append(quote.Items, line)
	}

	if len(validationErrors) != 0 {
		return models.Quote{}, validationErrors
	}
	return quote, nil
}

func quoteLine(item models.Item, requested models.QuoteItemRequest, field string) (models.QuoteLineItem, *models.VariantOption, *models.ValidationError) {
	line := models.QuoteLineItem{
		ItemID: item.ID, Name: item.Name, Quantity: requested.Quantity, UnitPriceCents: item.PriceCents,
	}
	if len(item.VariantGroups) == 0 {
		if requested.VariantOptionID != nil {
			return models.QuoteLineItem{}, nil, &models.ValidationError{
				Field: field + ".variant_option_id", Message: "item does not have variants",
			}
		}
		return line, nil, nil
	}
	if requested.VariantOptionID == nil {
		return models.QuoteLineItem{}, nil, &models.ValidationError{
			Field: field + ".variant_option_id", Message: "is required for this item",
		}
	}

	option, exists := findOption(item.VariantGroups[0].Options, *requested.VariantOptionID)
	if !exists {
		return models.QuoteLineItem{}, nil, &models.ValidationError{
			Field: field + ".variant_option_id", Message: "does not belong to this item",
		}
	}
	if !option.Available {
		return models.QuoteLineItem{}, nil, &models.ValidationError{
			Field: field + ".variant_option_id", Message: "variant is not available",
		}
	}

	line.UnitPriceCents = option.PriceCents
	line.Variant = &models.SelectedVariantOption{ID: option.ID, Name: option.Name}
	return line, &option, nil
}

func findOption(options []models.VariantOption, optionID int64) (models.VariantOption, bool) {
	for _, option := range options {
		if option.ID == optionID {
			return option, true
		}
	}
	return models.VariantOption{}, false
}

func stockAvailable(
	item models.Item,
	option *models.VariantOption,
	quantity int,
	itemTotals map[int64]int,
	variantTotals map[int64]int,
) bool {
	if option != nil && option.TrackStock {
		variantTotals[option.ID] += quantity
		return option.StockQuantity != nil && variantTotals[option.ID] <= *option.StockQuantity
	}
	if !item.TrackStock {
		return true
	}
	itemTotals[item.ID] += quantity
	return item.StockQuantity != nil && itemTotals[item.ID] <= *item.StockQuantity
}
