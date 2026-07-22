package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/shohinx/vanilla-api/internal/sdk/models"
	"github.com/shohinx/vanilla-api/internal/sdk/sqldb"
)

type fakeRepository struct {
	health      map[string]string
	snapshot    models.PublicMenuSnapshot
	snapshotErr error
	slug        string
}

func (f *fakeRepository) Health() map[string]string {
	return f.health
}

func (f *fakeRepository) PublicMenuSnapshot(_ context.Context, slug string) (models.PublicMenuSnapshot, error) {
	f.slug = slug
	return f.snapshot, f.snapshotErr
}

func TestReadiness(t *testing.T) {
	for name, test := range map[string]struct {
		health map[string]string
		status int
	}{
		"up":   {health: map[string]string{"status": "up"}, status: http.StatusOK},
		"down": {health: map[string]string{"status": "down"}, status: http.StatusServiceUnavailable},
	} {
		t.Run(name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/api/v1/health/readiness", nil)
			response := httptest.NewRecorder()

			New(&fakeRepository{health: test.health}).RegisterRoutes().ServeHTTP(response, request)

			if response.Code != test.status {
				t.Fatalf("status = %d, want %d", response.Code, test.status)
			}
		})
	}
}

func TestPublicMenuReturnsCompleteRestaurantSnapshot(t *testing.T) {
	generatedAt := time.Date(2026, time.July, 22, 18, 0, 0, 0, time.UTC)
	repository := &fakeRepository{snapshot: models.PublicMenuSnapshot{
		Payload:     []byte(`{"restaurant":{"name":"Vanilla Bakery"},"sections":[{"name":"Cakes","items":[]},{"name":"Drinks","items":[]}]}`),
		ETag:        "full-menu-v1",
		GeneratedAt: generatedAt,
	}}
	request := httptest.NewRequest(http.MethodGet, "/restaurants/vanilla-bakery/menu", nil)
	response := httptest.NewRecorder()

	New(repository).RegisterRoutes().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if repository.slug != "vanilla-bakery" {
		t.Fatalf("restaurant slug = %q, want %q", repository.slug, "vanilla-bakery")
	}
	if response.Header().Get("ETag") != `"full-menu-v1"` {
		t.Fatalf("ETag = %q", response.Header().Get("ETag"))
	}
	if response.Body.String() != string(repository.snapshot.Payload) {
		t.Fatalf("body = %s", response.Body.String())
	}
}

func TestPublicMenuNotFound(t *testing.T) {
	repository := &fakeRepository{snapshotErr: sqldb.ErrDBNotFound}
	request := httptest.NewRequest(http.MethodGet, "/restaurants/missing/menu", nil)
	response := httptest.NewRecorder()

	New(repository).RegisterRoutes().ServeHTTP(response, request)

	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNotFound)
	}
}

func TestPublicMenuUnavailable(t *testing.T) {
	repository := &fakeRepository{snapshotErr: errors.New("database unavailable")}
	request := httptest.NewRequest(http.MethodGet, "/restaurants/vanilla-bakery/menu", nil)
	response := httptest.NewRecorder()

	New(repository).RegisterRoutes().ServeHTTP(response, request)

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusInternalServerError)
	}
}
