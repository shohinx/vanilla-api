package menu

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/shohinx/vanilla-api/internal/sdk/models"
)

func NormalizeAndValidateNewItems(items []models.NewItem) ([]models.NewItem, error) {
	if len(items) == 0 {
		return nil, models.ValidationErrors{{Field: "items", Message: "must contain at least one item"}}
	}
	if len(items) > 100 {
		return nil, models.ValidationErrors{{Field: "items", Message: "cannot contain more than 100 items"}}
	}

	var validationErrors models.ValidationErrors
	for itemIndex := range items {
		item := &items[itemIndex]
		field := fmt.Sprintf("items[%d]", itemIndex)
		item.Name = strings.TrimSpace(item.Name)
		item.Description = strings.TrimSpace(item.Description)
		if item.ImageURL != nil {
			imageURL := strings.TrimSpace(*item.ImageURL)
			item.ImageURL = &imageURL
			if len(imageURL) > 2048 || !ValidImageURL(imageURL) {
				validationErrors = append(validationErrors, models.ValidationError{
					Field: field + ".image_url", Message: "must be empty, an absolute HTTP(S) URL, or a root-relative path",
				})
			}
		}
		if item.CategoryID < 1 {
			validationErrors = append(validationErrors, models.ValidationError{Field: field + ".category_id", Message: "must be a positive category ID"})
		}
		if item.Name == "" || len(item.Name) > 120 {
			validationErrors = append(validationErrors, models.ValidationError{Field: field + ".name", Message: "is required and cannot exceed 120 characters"})
		}
		if len(item.Description) > 1000 {
			validationErrors = append(validationErrors, models.ValidationError{Field: field + ".description", Message: "cannot exceed 1000 characters"})
		}
		if item.PriceCents < 0 {
			validationErrors = append(validationErrors, models.ValidationError{Field: field + ".price_cents", Message: "cannot be negative"})
		}
		validateStock(field, item.TrackStock, item.StockQuantity, &validationErrors)

		if item.VariantGroup == nil {
			continue
		}
		group := item.VariantGroup
		group.Name = strings.TrimSpace(group.Name)
		if group.Name == "" || len(group.Name) > 80 {
			validationErrors = append(validationErrors, models.ValidationError{Field: field + ".variant_group.name", Message: "is required and cannot exceed 80 characters"})
		}
		if group.Required == nil {
			required := true
			group.Required = &required
		}
		if !*group.Required {
			validationErrors = append(validationErrors, models.ValidationError{Field: field + ".variant_group.required", Message: "must be true; optional add-ons are not part of this menu model"})
		}
		if len(group.Options) == 0 {
			validationErrors = append(validationErrors, models.ValidationError{Field: field + ".variant_group.options", Message: "must contain at least one option"})
		}
		for optionIndex := range group.Options {
			option := &group.Options[optionIndex]
			optionField := fmt.Sprintf("%s.variant_group.options[%d]", field, optionIndex)
			option.Name = strings.TrimSpace(option.Name)
			if option.Name == "" || len(option.Name) > 80 {
				validationErrors = append(validationErrors, models.ValidationError{Field: optionField + ".name", Message: "is required and cannot exceed 80 characters"})
			}
			if option.PriceCents < 0 {
				validationErrors = append(validationErrors, models.ValidationError{Field: optionField + ".price_cents", Message: "cannot be negative"})
			}
			validateStock(optionField, option.TrackStock, option.StockQuantity, &validationErrors)
		}
	}
	if len(validationErrors) > 0 {
		return nil, validationErrors
	}
	return items, nil
}

func validateStock(field string, trackStock bool, stockQuantity *int, validationErrors *models.ValidationErrors) {
	if trackStock && stockQuantity == nil {
		*validationErrors = append(*validationErrors, models.ValidationError{Field: field + ".stock_qty", Message: "is required when track_stock is true"})
		return
	}
	if !trackStock && stockQuantity != nil {
		*validationErrors = append(*validationErrors, models.ValidationError{Field: field + ".stock_qty", Message: "must be omitted when track_stock is false"})
		return
	}
	if stockQuantity != nil && *stockQuantity < 0 {
		*validationErrors = append(*validationErrors, models.ValidationError{Field: field + ".stock_qty", Message: "cannot be negative"})
	}
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

func NormalizeAndValidateNewCategories(categories []models.NewCategory) ([]models.NewCategory, error) {
	if len(categories) == 0 {
		return nil, models.ValidationErrors{{Field: "categories", Message: "must contain at least one category"}}
	}
	if len(categories) > 50 {
		return nil, models.ValidationErrors{{Field: "categories", Message: "cannot contain more than 50 categories"}}
	}
	seen := make(map[string]bool, len(categories))
	var validationErrors models.ValidationErrors
	for index := range categories {
		category := &categories[index]
		field := fmt.Sprintf("categories[%d]", index)
		category.Name = strings.TrimSpace(category.Name)
		key := strings.ToLower(category.Name)
		if category.Name == "" || len(category.Name) > 80 {
			validationErrors = append(validationErrors, models.ValidationError{Field: field + ".name", Message: "is required and cannot exceed 80 characters"})
		} else if seen[key] {
			validationErrors = append(validationErrors, models.ValidationError{Field: field + ".name", Message: "must be unique within the request"})
		}
		seen[key] = true
	}
	if len(validationErrors) > 0 {
		return nil, validationErrors
	}
	return categories, nil
}

func BuildQuote(current models.Menu, request models.QuoteRequest) (models.Quote, error) {
	if len(request.Items) == 0 {
		return models.Quote{}, models.ValidationErrors{{Field: "items", Message: "must contain at least one item"}}
	}

	items := make(map[int64]models.Item)
	for _, category := range current.Categories {
		for _, item := range category.Items {
			items[item.ID] = item
		}
	}

	itemTotals := make(map[int64]int)
	variantTotals := make(map[int64]int)
	quote := models.Quote{Items: make([]models.QuoteLineItem, 0, len(request.Items))}
	var validationErrors models.ValidationErrors
	for index, requested := range request.Items {
		field := fmt.Sprintf("items[%d]", index)
		item, found := items[requested.ItemID]
		if !found {
			validationErrors = append(validationErrors, models.ValidationError{Field: field + ".item_id", Message: "item does not exist"})
			continue
		}
		if requested.Quantity < 1 {
			validationErrors = append(validationErrors, models.ValidationError{Field: field + ".quantity", Message: "must be at least 1"})
			continue
		}
		if !item.Available {
			validationErrors = append(validationErrors, models.ValidationError{Field: field + ".item_id", Message: "item is not available"})
			continue
		}

		unitPrice := item.PriceCents
		var selected *models.SelectedVariantOption
		var selectedOption *models.VariantOption
		if len(item.VariantGroups) > 0 {
			if requested.VariantOptionID == nil {
				validationErrors = append(validationErrors, models.ValidationError{Field: field + ".variant_option_id", Message: "is required for this item"})
				continue
			}
			for optionIndex := range item.VariantGroups[0].Options {
				option := &item.VariantGroups[0].Options[optionIndex]
				if option.ID == *requested.VariantOptionID {
					selectedOption = option
					break
				}
			}
			if selectedOption == nil {
				validationErrors = append(validationErrors, models.ValidationError{Field: field + ".variant_option_id", Message: "does not belong to this item"})
				continue
			}
			if !selectedOption.Available {
				validationErrors = append(validationErrors, models.ValidationError{Field: field + ".variant_option_id", Message: "variant is not available"})
				continue
			}
			unitPrice = selectedOption.PriceCents
			selected = &models.SelectedVariantOption{ID: selectedOption.ID, Name: selectedOption.Name}
		} else if requested.VariantOptionID != nil {
			validationErrors = append(validationErrors, models.ValidationError{Field: field + ".variant_option_id", Message: "item does not have variants"})
			continue
		}

		if selectedOption != nil && selectedOption.TrackStock {
			variantTotals[selectedOption.ID] += requested.Quantity
			if selectedOption.StockQuantity == nil || variantTotals[selectedOption.ID] > *selectedOption.StockQuantity {
				validationErrors = append(validationErrors, models.ValidationError{Field: field + ".quantity", Message: "requested variant quantity is not available"})
				continue
			}
		} else if item.TrackStock {
			itemTotals[item.ID] += requested.Quantity
			if item.StockQuantity == nil || itemTotals[item.ID] > *item.StockQuantity {
				validationErrors = append(validationErrors, models.ValidationError{Field: field + ".quantity", Message: "requested quantity is not available"})
				continue
			}
		}

		lineTotal := unitPrice * requested.Quantity
		quote.Items = append(quote.Items, models.QuoteLineItem{
			ItemID: item.ID, Name: item.Name, Variant: selected, Quantity: requested.Quantity,
			UnitPriceCents: unitPrice, LineTotalCents: lineTotal,
		})
		quote.TotalCents += lineTotal
	}
	if len(validationErrors) > 0 {
		return models.Quote{}, validationErrors
	}
	return quote, nil
}
