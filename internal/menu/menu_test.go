package menu

import (
	"testing"

	"github.com/shohinx/vanilla-api/internal/sdk/models"
)

func intPointer(value int) *int       { return &value }
func int64Pointer(value int64) *int64 { return &value }

func TestBuildQuoteUsesOneRequiredVariant(t *testing.T) {
	current := models.Menu{Categories: []models.Category{{Items: []models.Item{latte()}}}}
	quote, err := BuildQuote(current, models.QuoteRequest{Items: []models.QuoteItemRequest{{
		ItemID: 1, Quantity: 2, VariantOptionID: int64Pointer(12),
	}}})
	if err != nil {
		t.Fatalf("BuildQuote() returned an error: %v", err)
	}
	if quote.TotalCents != 1250 || quote.Items[0].UnitPriceCents != 625 || quote.Items[0].Variant.Name != "Large" {
		t.Fatalf("unexpected quote: %+v", quote)
	}
}

func TestBuildQuoteRejectsMissingRequiredVariant(t *testing.T) {
	current := models.Menu{Categories: []models.Category{{Items: []models.Item{latte()}}}}
	_, err := BuildQuote(current, models.QuoteRequest{Items: []models.QuoteItemRequest{{ItemID: 1, Quantity: 1}}})
	if err == nil {
		t.Fatal("expected a required variant to be enforced")
	}
}

func TestBuildQuoteKeepsSeparateMenuItemsSeparate(t *testing.T) {
	current := models.Menu{Categories: []models.Category{{Items: []models.Item{
		latte(),
		{ID: 2, Name: "Brown Butter Cookie", PriceCents: 425, Available: true, TrackStock: true, StockQuantity: intPointer(4)},
	}}}}
	quote, err := BuildQuote(current, models.QuoteRequest{Items: []models.QuoteItemRequest{
		{ItemID: 1, Quantity: 1, VariantOptionID: int64Pointer(11)},
		{ItemID: 2, Quantity: 2},
	}})
	if err != nil {
		t.Fatalf("BuildQuote() returned an error: %v", err)
	}
	if len(quote.Items) != 2 || quote.Items[1].Name != "Brown Butter Cookie" || quote.Items[1].Variant != nil {
		t.Fatalf("expected cookie to be its own unmodified line: %+v", quote.Items)
	}
}

func TestBuildQuoteRejectsCombinedInsufficientStock(t *testing.T) {
	current := models.Menu{Categories: []models.Category{{Items: []models.Item{{
		ID: 2, Name: "Croissant", PriceCents: 475, Available: true,
		TrackStock: true, StockQuantity: intPointer(2),
	}}}}}
	_, err := BuildQuote(current, models.QuoteRequest{Items: []models.QuoteItemRequest{
		{ItemID: 2, Quantity: 1}, {ItemID: 2, Quantity: 2},
	}})
	if err == nil {
		t.Fatal("expected combined quantity to be checked against stock")
	}
}

func TestBuildQuoteDoesNotRequireStockForMadeToOrderItem(t *testing.T) {
	current := models.Menu{Categories: []models.Category{{Items: []models.Item{{
		ID: 3, Name: "Soup", PriceCents: 800, Available: true, TrackStock: false,
	}}}}}
	if _, err := BuildQuote(current, models.QuoteRequest{Items: []models.QuoteItemRequest{{ItemID: 3, Quantity: 50}}}); err != nil {
		t.Fatalf("made-to-order item should not have a stock limit: %v", err)
	}
}

func TestNormalizeAndValidateNewItemsEnforcesStockShape(t *testing.T) {
	_, err := NormalizeAndValidateNewItems([]models.NewItem{{
		CategoryID: 1, Name: "Cookie", PriceCents: 350, TrackStock: true,
	}})
	if err == nil {
		t.Fatal("expected stock_qty when track_stock is true")
	}

	items, err := NormalizeAndValidateNewItems([]models.NewItem{{
		CategoryID: 1, Name: "Cookie", PriceCents: 350, TrackStock: true, StockQuantity: intPointer(5),
	}})
	if err != nil || items[0].StockQuantity == nil {
		t.Fatalf("expected valid tracked item: items=%+v err=%v", items, err)
	}
}

func TestNormalizeAndValidateNewItemsRejectsOptionalAddOnGroup(t *testing.T) {
	required := false
	_, err := NormalizeAndValidateNewItems([]models.NewItem{{
		CategoryID: 1, Name: "Latte", PriceCents: 550,
		VariantGroup: &models.NewVariantGroup{Name: "Extras", Required: &required, Options: []models.NewVariantOption{{Name: "Cookie", PriceCents: 425}}},
	}})
	if err == nil {
		t.Fatal("expected optional add-ons to be rejected by the simplified model")
	}
}

func TestNormalizeAndValidateNewItemsTrimsOptionalImageURL(t *testing.T) {
	imageURL := " /api/v1/images/menu/cookie.webp "
	items, err := NormalizeAndValidateNewItems([]models.NewItem{{
		CategoryID: 1, Name: "Cookie", PriceCents: 350, ImageURL: &imageURL,
	}})
	if err != nil {
		t.Fatalf("NormalizeAndValidateNewItems() returned an error: %v", err)
	}
	if items[0].ImageURL == nil || *items[0].ImageURL != "/api/v1/images/menu/cookie.webp" {
		t.Fatalf("image URL was not normalized: %+v", items[0].ImageURL)
	}
}

func TestNormalizeAndValidateNewCategories(t *testing.T) {
	categories, err := NormalizeAndValidateNewCategories([]models.NewCategory{{Name: " Cold Beverages "}})
	if err != nil {
		t.Fatalf("NormalizeAndValidateNewCategories() returned an error: %v", err)
	}
	if categories[0].Name != "Cold Beverages" {
		t.Fatalf("category was not normalized: %+v", categories[0])
	}
}

func latte() models.Item {
	return models.Item{
		ID: 1, Name: "Latte", PriceCents: 550, Available: true,
		VariantGroups: []models.VariantGroup{{
			ID: 10, Name: "Size", Required: true,
			Options: []models.VariantOption{
				{ID: 11, Name: "Small", PriceCents: 550, Available: true},
				{ID: 12, Name: "Large", PriceCents: 625, Available: true},
			},
		}},
	}
}
