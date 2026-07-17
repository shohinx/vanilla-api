package menu

import (
	"testing"

	"github.com/shohinx/vanilla-api/internal/sdk/models"
)

func TestBuildQuoteRejectsDuplicateOptions(t *testing.T) {
	current := models.Menu{Categories: []models.Category{{Items: []models.Item{{
		ID: "latte", Name: "Latte", Currency: "USD", PriceCents: 500,
		Available: true, QuantityAvailable: 2,
		ModifierGroups: []models.ModifierGroup{{
			ID: "size", Name: "Size", MinSelections: 1, MaxSelections: 1,
			Options: []models.ModifierOption{{ID: "small", Name: "Small", Available: true}},
		}},
	}}}}}

	_, err := BuildQuote(current, models.QuoteRequest{Items: []models.QuoteItemRequest{{
		ItemID: "latte", Quantity: 1, OptionIDs: []string{"small", "small"},
	}}})
	if err == nil {
		t.Fatal("expected duplicate options to be rejected")
	}
}

func TestBuildQuoteRejectsInsufficientStock(t *testing.T) {
	current := models.Menu{Categories: []models.Category{{Items: []models.Item{{
		ID: "croissant", Name: "Croissant", Currency: "USD", PriceCents: 475,
		Available: true, QuantityAvailable: 1,
	}}}}}

	_, err := BuildQuote(current, models.QuoteRequest{Items: []models.QuoteItemRequest{{
		ItemID: "croissant", Quantity: 2,
	}}})
	if err == nil {
		t.Fatal("expected insufficient stock to be rejected")
	}
}

func TestBuildQuoteAggregatesStockAcrossCustomizedLines(t *testing.T) {
	current := models.Menu{Categories: []models.Category{{Items: []models.Item{{
		ID: "latte", Name: "Latte", Currency: "USD", PriceCents: 550,
		Available: true, QuantityAvailable: 2,
	}}}}}

	_, err := BuildQuote(current, models.QuoteRequest{Items: []models.QuoteItemRequest{
		{ItemID: "latte", Quantity: 2},
		{ItemID: "latte", Quantity: 1},
	}})
	if err == nil {
		t.Fatal("expected total quantity across line items to be checked against stock")
	}
}

func TestNormalizeAndValidateNewItemsRejectsDuplicateIDs(t *testing.T) {
	_, err := NormalizeAndValidateNewItems([]models.NewItem{
		{ID: "cookie", CategoryID: "sweets", Name: "Cookie", Type: "sweet", Currency: "USD"},
		{ID: "cookie", CategoryID: "sweets", Name: "Another Cookie", Type: "sweet", Currency: "USD"},
	})
	if err == nil {
		t.Fatal("expected duplicate item IDs to be rejected")
	}
}

func TestNormalizeAndValidateNewItemsKeepsImageOptional(t *testing.T) {
	items, err := NormalizeAndValidateNewItems([]models.NewItem{{
		ID: "cookie", CategoryID: "sweets", Name: "Cookie", Type: "sweet",
		Currency: "USD",
	}})
	if err != nil {
		t.Fatalf("NormalizeAndValidateNewItems() returned an error: %v", err)
	}
	if items[0].ImageURL != nil {
		t.Fatalf("expected an omitted image URL to remain nil, got %q", *items[0].ImageURL)
	}
}

func TestNormalizeAndValidateNewItemsTrimsImageURL(t *testing.T) {
	imageURL := " /api/v1/images/menu/cookie.webp "
	items, err := NormalizeAndValidateNewItems([]models.NewItem{{
		ID: "cookie", CategoryID: "sweets", Name: "Cookie", Type: "sweet",
		Currency: "USD", ImageURL: &imageURL,
	}})
	if err != nil {
		t.Fatalf("NormalizeAndValidateNewItems() returned an error: %v", err)
	}
	if items[0].ImageURL == nil || *items[0].ImageURL != "/api/v1/images/menu/cookie.webp" {
		t.Fatalf("image URL was not normalized: %+v", items[0].ImageURL)
	}
}

func TestNormalizeAndValidateNewItemsRejectsInvalidOptionalImageURL(t *testing.T) {
	imageURL := "not-a-url"
	_, err := NormalizeAndValidateNewItems([]models.NewItem{{
		ID: "cookie", CategoryID: "sweets", Name: "Cookie", Type: "sweet",
		Currency: "USD", ImageURL: &imageURL,
	}})
	if err == nil {
		t.Fatal("expected an invalid optional image URL to be rejected")
	}
}

func TestNormalizeAndValidateNewCategories(t *testing.T) {
	categories, err := NormalizeAndValidateNewCategories([]models.NewCategory{{
		ID: "cold-beverages", Name: " Cold Beverages ", Description: " Iced drinks ",
	}})
	if err != nil {
		t.Fatalf("NormalizeAndValidateNewCategories() returned an error: %v", err)
	}
	if categories[0].Name != "Cold Beverages" || categories[0].Description != "Iced drinks" {
		t.Fatalf("category was not normalized: %+v", categories[0])
	}
}
