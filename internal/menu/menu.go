package menu

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

var slugPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

type Menu struct {
	GeneratedAt time.Time  `json:"generated_at"`
	Categories  []Category `json:"categories"`
}

type Category struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Items       []Item `json:"items"`
}

type MenuCategory struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	SortOrder   int    `json:"sort_order"`
}

type NewCategory struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	SortOrder   int    `json:"sort_order,omitempty"`
}

type Item struct {
	ID                string          `json:"id"`
	Name              string          `json:"name"`
	Description       string          `json:"description,omitempty"`
	Type              string          `json:"type"`
	ImageURL          string          `json:"image_url,omitempty"`
	PriceCents        int             `json:"price_cents"`
	Currency          string          `json:"currency"`
	Available         bool            `json:"available"`
	QuantityAvailable int             `json:"-"`
	ModifierGroups    []ModifierGroup `json:"modifier_groups,omitempty"`
}

type ModifierGroup struct {
	ID            string           `json:"id"`
	Name          string           `json:"name"`
	MinSelections int              `json:"min_selections"`
	MaxSelections int              `json:"max_selections"`
	Options       []ModifierOption `json:"options"`
}

type ModifierOption struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	PriceDeltaCents int    `json:"price_delta_cents"`
	Available       bool   `json:"available"`
}

type NewItem struct {
	ID             string             `json:"id"`
	CategoryID     string             `json:"category_id"`
	Name           string             `json:"name"`
	Description    string             `json:"description,omitempty"`
	Type           string             `json:"type"`
	ImageURL       string             `json:"image_url,omitempty"`
	PriceCents     int                `json:"price_cents"`
	Currency       string             `json:"currency"`
	Quantity       int                `json:"quantity"`
	SortOrder      int                `json:"sort_order,omitempty"`
	ModifierGroups []NewModifierGroup `json:"modifier_groups,omitempty"`
}

type NewModifierGroup struct {
	ID            string              `json:"id"`
	Name          string              `json:"name"`
	MinSelections int                 `json:"min_selections"`
	MaxSelections int                 `json:"max_selections"`
	SortOrder     int                 `json:"sort_order,omitempty"`
	Options       []NewModifierOption `json:"options"`
}

type NewModifierOption struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	PriceDeltaCents int    `json:"price_delta_cents"`
	Available       *bool  `json:"available,omitempty"`
	SortOrder       int    `json:"sort_order,omitempty"`
}

type Inventory struct {
	ItemID    string    `json:"item_id"`
	Quantity  int       `json:"quantity"`
	Available bool      `json:"available"`
	UpdatedAt time.Time `json:"updated_at"`
}

type QuoteRequest struct {
	Items []QuoteItemRequest `json:"items"`
}

type SubmitOrderRequest struct {
	CustomerName string             `json:"customer_name"`
	Notes        string             `json:"notes,omitempty"`
	Items        []QuoteItemRequest `json:"items"`
}

type QuoteItemRequest struct {
	ItemID    string   `json:"item_id"`
	Quantity  int      `json:"quantity"`
	OptionIDs []string `json:"option_ids,omitempty"`
}

type Quote struct {
	Currency      string          `json:"currency"`
	SubtotalCents int             `json:"subtotal_cents"`
	Items         []QuoteLineItem `json:"items"`
}

type QuoteLineItem struct {
	ItemID         string                `json:"item_id"`
	Name           string                `json:"name"`
	Quantity       int                   `json:"quantity"`
	UnitPriceCents int                   `json:"unit_price_cents"`
	LineTotalCents int                   `json:"line_total_cents"`
	Options        []SelectedQuoteOption `json:"options,omitempty"`
}

type SelectedQuoteOption struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	PriceDeltaCents int    `json:"price_delta_cents"`
}

type Order struct {
	ID            string          `json:"id"`
	OrderNumber   string          `json:"order_number"`
	CustomerName  string          `json:"customer_name"`
	Notes         string          `json:"notes,omitempty"`
	Status        string          `json:"status"`
	Currency      string          `json:"currency"`
	SubtotalCents int             `json:"subtotal_cents"`
	Items         []QuoteLineItem `json:"items"`
	CreatedAt     time.Time       `json:"created_at"`
	SoldAt        *time.Time      `json:"sold_at,omitempty"`
}

type ValidationError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

type ValidationErrors []ValidationError

func (e ValidationErrors) Error() string {
	return "request validation failed"
}

func NormalizeAndValidateNewItems(items []NewItem) ([]NewItem, error) {
	if len(items) == 0 {
		return nil, ValidationErrors{{Field: "items", Message: "must contain at least one item"}}
	}
	if len(items) > 100 {
		return nil, ValidationErrors{{Field: "items", Message: "cannot contain more than 100 items"}}
	}

	seenIDs := make(map[string]bool)
	var validationErrors ValidationErrors
	for itemIndex := range items {
		item := &items[itemIndex]
		field := fmt.Sprintf("items[%d]", itemIndex)
		item.ID = strings.TrimSpace(item.ID)
		item.CategoryID = strings.TrimSpace(item.CategoryID)
		item.Name = strings.TrimSpace(item.Name)
		item.Description = strings.TrimSpace(item.Description)
		item.Type = strings.ToLower(strings.TrimSpace(item.Type))
		item.ImageURL = strings.TrimSpace(item.ImageURL)
		item.Currency = strings.ToUpper(strings.TrimSpace(item.Currency))
		if !slugPattern.MatchString(item.ID) {
			validationErrors = append(validationErrors, ValidationError{Field: field + ".id", Message: "must be a lowercase slug such as chocolate-cake"})
		} else if seenIDs[item.ID] {
			validationErrors = append(validationErrors, ValidationError{Field: field + ".id", Message: "must be unique within the request"})
		}
		seenIDs[item.ID] = true
		if !slugPattern.MatchString(item.CategoryID) {
			validationErrors = append(validationErrors, ValidationError{Field: field + ".category_id", Message: "must be a valid category slug"})
		}
		if item.Name == "" || len(item.Name) > 120 {
			validationErrors = append(validationErrors, ValidationError{Field: field + ".name", Message: "is required and cannot exceed 120 characters"})
		}
		if item.Type != "pastry" && item.Type != "cake" && item.Type != "sweet" && item.Type != "drink" {
			validationErrors = append(validationErrors, ValidationError{Field: field + ".type", Message: "must be pastry, cake, sweet, or drink"})
		}
		if item.PriceCents < 0 {
			validationErrors = append(validationErrors, ValidationError{Field: field + ".price_cents", Message: "cannot be negative"})
		}
		if len(item.Currency) != 3 {
			validationErrors = append(validationErrors, ValidationError{Field: field + ".currency", Message: "must be a three-letter currency code such as USD"})
		}
		if item.Quantity < 0 {
			validationErrors = append(validationErrors, ValidationError{Field: field + ".quantity", Message: "cannot be negative"})
		}

		for groupIndex := range item.ModifierGroups {
			group := &item.ModifierGroups[groupIndex]
			groupField := fmt.Sprintf("%s.modifier_groups[%d]", field, groupIndex)
			group.ID = strings.TrimSpace(group.ID)
			group.Name = strings.TrimSpace(group.Name)
			if !slugPattern.MatchString(group.ID) {
				validationErrors = append(validationErrors, ValidationError{Field: groupField + ".id", Message: "must be a lowercase slug"})
			} else if seenIDs[group.ID] {
				validationErrors = append(validationErrors, ValidationError{Field: groupField + ".id", Message: "must be unique within the request"})
			}
			seenIDs[group.ID] = true
			if group.Name == "" {
				validationErrors = append(validationErrors, ValidationError{Field: groupField + ".name", Message: "is required"})
			}
			if group.MinSelections < 0 || group.MaxSelections < group.MinSelections || group.MaxSelections > len(group.Options) {
				validationErrors = append(validationErrors, ValidationError{Field: groupField, Message: "selection limits must be valid for the available options"})
			}
			if len(group.Options) == 0 {
				validationErrors = append(validationErrors, ValidationError{Field: groupField + ".options", Message: "must contain at least one option"})
			}
			for optionIndex := range group.Options {
				option := &group.Options[optionIndex]
				optionField := fmt.Sprintf("%s.options[%d]", groupField, optionIndex)
				option.ID = strings.TrimSpace(option.ID)
				option.Name = strings.TrimSpace(option.Name)
				if !slugPattern.MatchString(option.ID) {
					validationErrors = append(validationErrors, ValidationError{Field: optionField + ".id", Message: "must be a lowercase slug"})
				} else if seenIDs[option.ID] {
					validationErrors = append(validationErrors, ValidationError{Field: optionField + ".id", Message: "must be unique within the request"})
				}
				seenIDs[option.ID] = true
				if option.Name == "" {
					validationErrors = append(validationErrors, ValidationError{Field: optionField + ".name", Message: "is required"})
				}
			}
		}
	}
	if len(validationErrors) > 0 {
		return nil, validationErrors
	}
	return items, nil
}

func NormalizeAndValidateNewCategories(categories []NewCategory) ([]NewCategory, error) {
	if len(categories) == 0 {
		return nil, ValidationErrors{{Field: "categories", Message: "must contain at least one category"}}
	}
	if len(categories) > 50 {
		return nil, ValidationErrors{{Field: "categories", Message: "cannot contain more than 50 categories"}}
	}
	seen := make(map[string]bool, len(categories))
	var validationErrors ValidationErrors
	for index := range categories {
		category := &categories[index]
		field := fmt.Sprintf("categories[%d]", index)
		category.ID = strings.TrimSpace(category.ID)
		category.Name = strings.TrimSpace(category.Name)
		category.Description = strings.TrimSpace(category.Description)
		if !slugPattern.MatchString(category.ID) {
			validationErrors = append(validationErrors, ValidationError{Field: field + ".id", Message: "must be a lowercase slug such as cold-beverages"})
		} else if seen[category.ID] {
			validationErrors = append(validationErrors, ValidationError{Field: field + ".id", Message: "must be unique within the request"})
		}
		seen[category.ID] = true
		if category.Name == "" || len(category.Name) > 80 {
			validationErrors = append(validationErrors, ValidationError{Field: field + ".name", Message: "is required and cannot exceed 80 characters"})
		}
		if len(category.Description) > 300 {
			validationErrors = append(validationErrors, ValidationError{Field: field + ".description", Message: "cannot exceed 300 characters"})
		}
	}
	if len(validationErrors) > 0 {
		return nil, validationErrors
	}
	return categories, nil
}

func BuildQuote(current Menu, request QuoteRequest) (Quote, error) {
	if len(request.Items) == 0 {
		return Quote{}, ValidationErrors{{Field: "items", Message: "must contain at least one item"}}
	}

	items := make(map[string]Item)
	for _, category := range current.Categories {
		for _, item := range category.Items {
			items[item.ID] = item
		}
	}
	requestedTotals := make(map[string]int, len(request.Items))
	for _, requested := range request.Items {
		if requested.Quantity > 0 {
			requestedTotals[requested.ItemID] += requested.Quantity
		}
	}

	quote := Quote{Items: make([]QuoteLineItem, 0, len(request.Items))}
	var validationErrors ValidationErrors
	for index, requested := range request.Items {
		field := fmt.Sprintf("items[%d]", index)
		item, found := items[requested.ItemID]
		if !found {
			validationErrors = append(validationErrors, ValidationError{Field: field + ".item_id", Message: "item does not exist"})
			continue
		}
		if requested.Quantity < 1 {
			validationErrors = append(validationErrors, ValidationError{Field: field + ".quantity", Message: "must be at least 1"})
			continue
		}
		if !item.Available || requestedTotals[item.ID] > item.QuantityAvailable {
			validationErrors = append(validationErrors, ValidationError{Field: field + ".quantity", Message: "requested quantity is not available"})
			continue
		}

		selected, optionTotal, err := validateOptions(item, requested.OptionIDs, field)
		if err != nil {
			var optionErrors ValidationErrors
			if errors.As(err, &optionErrors) {
				validationErrors = append(validationErrors, optionErrors...)
			}
			continue
		}

		unitPrice := item.PriceCents + optionTotal
		lineTotal := unitPrice * requested.Quantity
		if quote.Currency == "" {
			quote.Currency = item.Currency
		} else if quote.Currency != item.Currency {
			validationErrors = append(validationErrors, ValidationError{Field: field + ".item_id", Message: "item currency does not match the order"})
			continue
		}

		quote.Items = append(quote.Items, QuoteLineItem{
			ItemID:         item.ID,
			Name:           item.Name,
			Quantity:       requested.Quantity,
			UnitPriceCents: unitPrice,
			LineTotalCents: lineTotal,
			Options:        selected,
		})
		quote.SubtotalCents += lineTotal
	}

	if len(validationErrors) > 0 {
		return Quote{}, validationErrors
	}
	return quote, nil
}

func validateOptions(item Item, optionIDs []string, field string) ([]SelectedQuoteOption, int, error) {
	selectedIDs := make(map[string]bool, len(optionIDs))
	for _, id := range optionIDs {
		if selectedIDs[id] {
			return nil, 0, ValidationErrors{{Field: field + ".option_ids", Message: "cannot contain duplicate options"}}
		}
		selectedIDs[id] = true
	}

	knownSelections := 0
	priceDelta := 0
	selected := make([]SelectedQuoteOption, 0, len(optionIDs))
	var validationErrors ValidationErrors
	for _, group := range item.ModifierGroups {
		groupSelections := 0
		for _, option := range group.Options {
			if !selectedIDs[option.ID] {
				continue
			}
			knownSelections++
			groupSelections++
			if !option.Available {
				validationErrors = append(validationErrors, ValidationError{Field: field + ".option_ids", Message: fmt.Sprintf("option %q is unavailable", option.ID)})
				continue
			}
			selected = append(selected, SelectedQuoteOption{ID: option.ID, Name: option.Name, PriceDeltaCents: option.PriceDeltaCents})
			priceDelta += option.PriceDeltaCents
		}
		if groupSelections < group.MinSelections {
			validationErrors = append(validationErrors, ValidationError{Field: field + ".option_ids", Message: fmt.Sprintf("select at least %d option(s) from %s", group.MinSelections, group.Name)})
		}
		if group.MaxSelections > 0 && groupSelections > group.MaxSelections {
			validationErrors = append(validationErrors, ValidationError{Field: field + ".option_ids", Message: fmt.Sprintf("select at most %d option(s) from %s", group.MaxSelections, group.Name)})
		}
	}
	if knownSelections != len(optionIDs) {
		validationErrors = append(validationErrors, ValidationError{Field: field + ".option_ids", Message: "contains an option that does not belong to this item"})
	}
	if len(validationErrors) > 0 {
		return nil, 0, validationErrors
	}
	return selected, priceDelta, nil
}
