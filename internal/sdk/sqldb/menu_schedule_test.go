package sqldb

import (
	"errors"
	"testing"

	"github.com/shohinx/vanilla-api/internal/sdk/models"
)

func TestNormalizeMenuInputUsesDirectActiveState(t *testing.T) {
	input, err := normalizeMenuInput(models.MenuInput{Name: "Breakfast", Code: "breakfast"})
	if err != nil {
		t.Fatalf("normalize menu: %v", err)
	}
	if input.Status != "active" {
		t.Fatalf("status = %q, want active", input.Status)
	}
	if _, err := normalizeMenuInput(models.MenuInput{Name: "Breakfast", Code: "breakfast", Status: "draft"}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("draft status error = %v, want ErrInvalidInput", err)
	}
}

func TestNormalizeMenuScheduleInput(t *testing.T) {
	t.Run("defaults and normalizes", func(t *testing.T) {
		input, err := normalizeMenuScheduleInput(models.MenuScheduleInput{
			StartLocalTime: "8:00",
			EndLocalTime:   "14:30",
		})
		if err != nil {
			t.Fatalf("normalize schedule: %v", err)
		}
		if input.WeekdayMask != 127 || input.Status != "active" {
			t.Fatalf("defaults = weekday_mask %d, status %q", input.WeekdayMask, input.Status)
		}
		if input.StartLocalTime != "08:00:00" || input.EndLocalTime != "14:30:00" {
			t.Fatalf("times = %q to %q", input.StartLocalTime, input.EndLocalTime)
		}
	})

	for name, input := range map[string]models.MenuScheduleInput{
		"weekday mask too large": {WeekdayMask: 128},
		"invalid status":         {Status: "archived"},
		"end date before start":  {StartDate: "2026-08-02", EndDate: "2026-08-01"},
		"only start time":        {StartLocalTime: "08:00"},
		"equal times":            {StartLocalTime: "08:00", EndLocalTime: "08:00"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := normalizeMenuScheduleInput(input); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("error = %v, want ErrInvalidInput", err)
			}
		})
	}
}
