package sqldb

import (
	"errors"
	"fmt"
	"testing"

	"github.com/shohinx/vanilla-api/internal/sdk/models"
)

func TestNormalizeCatalogItemInput(t *testing.T) {
	input, err := normalizeCatalogItemInput(models.CatalogItemInput{
		Name:        "  Cappuccino  ",
		Description: "  Espresso and steamed milk.  ",
		Variants: []models.CatalogItemVariant{
			{Name: "  Small  ", PriceCents: 450},
			{Name: "Large", PriceCents: 600},
		},
	})
	if err != nil {
		t.Fatalf("normalize catalog item: %v", err)
	}
	if input.Name != "Cappuccino" || input.Description != "Espresso and steamed milk." || input.Status != "active" {
		t.Fatalf("normalized catalog item = %+v", input)
	}
	if len(input.Variants) != 2 || input.Variants[0].Name != "Small" || input.Variants[1].PriceCents != 600 {
		t.Fatalf("normalized variants = %+v", input.Variants)
	}

	tooManyVariants := make([]models.CatalogItemVariant, 21)
	for i := range tooManyVariants {
		tooManyVariants[i] = models.CatalogItemVariant{Name: fmt.Sprintf("Size %d", i), PriceCents: 100}
	}

	for name, invalid := range map[string]models.CatalogItemInput{
		"missing name":           {},
		"negative item price":    {Name: "Croissant", PriceCents: -1},
		"unsupported status":     {Name: "Croissant", Status: "archived"},
		"too many variants":      {Name: "Coffee", Variants: tooManyVariants},
		"missing variant name":   {Name: "Coffee", Variants: []models.CatalogItemVariant{{PriceCents: 450}}},
		"negative variant price": {Name: "Coffee", Variants: []models.CatalogItemVariant{{Name: "Small", PriceCents: -1}}},
		"duplicate variant name": {Name: "Coffee", Variants: []models.CatalogItemVariant{{Name: "Small"}, {Name: " small "}}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := normalizeCatalogItemInput(invalid); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("error = %v, want ErrInvalidInput", err)
			}
		})
	}
}

func TestNormalizeCatalogItemInputUsesEmptyVariantArray(t *testing.T) {
	input, err := normalizeCatalogItemInput(models.CatalogItemInput{Name: "Croissant"})
	if err != nil {
		t.Fatalf("normalize catalog item: %v", err)
	}
	if input.Variants == nil || len(input.Variants) != 0 {
		t.Fatalf("variants = %#v, want non-nil empty slice", input.Variants)
	}
}
