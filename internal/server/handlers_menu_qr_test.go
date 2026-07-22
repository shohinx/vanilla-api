package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/shohinx/vanilla-api/internal/services/dub"
)

type fakeMenuLinkRepository struct {
	link      dub.Link
	err       error
	domain    string
	linkKey   string
	callCount int
}

func (f *fakeMenuLinkRepository) RetrieveMenuLinkByKey(_ context.Context, domain, linkKey string) (dub.Link, error) {
	f.callCount++
	f.domain = domain
	f.linkKey = linkKey
	return f.link, f.err
}

func TestGetMenuQR(t *testing.T) {
	gin.SetMode(gin.TestMode)

	for name, test := range map[string]struct {
		menuLinks menuLinkRepository
		domain    string
		linkKey   string
		status    int
		errorCode string
	}{
		"not configured": {
			status:    http.StatusServiceUnavailable,
			errorCode: "menu_qr_not_configured",
		},
		"not found": {
			menuLinks: &fakeMenuLinkRepository{err: dub.ErrNotFound},
			domain:    "dub.sh",
			linkKey:   "menu",
			status:    http.StatusNotFound,
			errorCode: "menu_qr_not_found",
		},
		"Dub unavailable": {
			menuLinks: &fakeMenuLinkRepository{err: errors.New("upstream unavailable")},
			domain:    "dub.sh",
			linkKey:   "menu",
			status:    http.StatusBadGateway,
			errorCode: "dub_unavailable",
		},
	} {
		t.Run(name, func(t *testing.T) {
			service := New(&fakeRepository{})
			service.menuLinks = test.menuLinks
			service.dubDomain = test.domain
			service.dubLinkKey = test.linkKey

			response := serveMenuQR(t, service)
			if response.Code != test.status {
				t.Fatalf("status = %d, want %d", response.Code, test.status)
			}

			var body map[string]string
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if body["error"] != test.errorCode {
				t.Fatalf("error = %q, want %q", body["error"], test.errorCode)
			}
		})
	}
}

func TestGetMenuQRReturnsConfiguredDubLink(t *testing.T) {
	gin.SetMode(gin.TestMode)
	menuLinks := &fakeMenuLinkRepository{link: dub.Link{
		ShortURL:  "https://dub.sh/menu",
		QRCodeURL: "https://api.dub.co/qr?url=https://dub.sh/menu",
		URL:       "https://menu.example.com",
	}}
	service := New(&fakeRepository{})
	service.menuLinks = menuLinks
	service.dubDomain = "dub.sh"
	service.dubLinkKey = "menu"

	response := serveMenuQR(t, service)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if menuLinks.callCount != 1 || menuLinks.domain != "dub.sh" || menuLinks.linkKey != "menu" {
		t.Fatalf("retrieve arguments = (%q, %q), calls = %d", menuLinks.domain, menuLinks.linkKey, menuLinks.callCount)
	}

	var body menuQRResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.ShortURL != menuLinks.link.ShortURL || body.QRCodeURL != menuLinks.link.QRCodeURL || body.DestinationURL != menuLinks.link.URL {
		t.Fatalf("response = %+v", body)
	}
}

func serveMenuQR(t *testing.T, service *Server) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/menus/qr", nil)
	response := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(response)
	context.Request = request
	service.GetMenuQR(context)
	return response
}
