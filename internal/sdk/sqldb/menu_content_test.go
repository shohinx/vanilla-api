package sqldb

import (
	"errors"
	"testing"

	"github.com/shohinx/vanilla-api/internal/sdk/models"
)

func TestNormalizeMenuSectionInput(t *testing.T) {
	input, err := normalizeMenuSectionInput(models.MenuSectionInput{
		Name:        "  Coffee  ",
		Description: "  Espresso-based drinks.  ",
		SortOrder:   10,
	})
	if err != nil {
		t.Fatalf("normalize menu section: %v", err)
	}
	if input.Name != "Coffee" || input.Description != "Espresso-based drinks." || input.Status != "active" {
		t.Fatalf("normalized section = %+v", input)
	}

	for name, invalid := range map[string]models.MenuSectionInput{
		"missing name":        {},
		"negative sort order": {Name: "Coffee", SortOrder: -1},
		"unsupported status":  {Name: "Coffee", Status: "archived"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := normalizeMenuSectionInput(invalid); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("error = %v, want ErrInvalidInput", err)
			}
		})
	}
}

func TestNormalizeMenuEntryInput(t *testing.T) {
	input, err := normalizeMenuEntryInput(models.MenuEntryInput{
		CatalogItemID: "  90000000-0000-4000-8000-000000000003  ",
		SortOrder:     10,
	})
	if err != nil {
		t.Fatalf("normalize menu entry: %v", err)
	}
	if input.CatalogItemID != "90000000-0000-4000-8000-000000000003" || input.Status != "active" {
		t.Fatalf("normalized entry = %+v", input)
	}

	for name, invalid := range map[string]models.MenuEntryInput{
		"missing catalog item": {},
		"invalid catalog item": {CatalogItemID: "not-a-uuid"},
		"negative sort order":  {CatalogItemID: "90000000-0000-4000-8000-000000000003", SortOrder: -1},
		"unsupported status":   {CatalogItemID: "90000000-0000-4000-8000-000000000003", Status: "archived"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := normalizeMenuEntryInput(invalid); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("error = %v, want ErrInvalidInput", err)
			}
		})
	}
}
