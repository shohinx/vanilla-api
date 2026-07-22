package dub

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCreateMenuLinkUsesBearerTokenAndDefaults(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/links" {
			t.Fatalf("unexpected request: %s %s", request.Method, request.URL.String())
		}
		if request.Header.Get("Authorization") != "Bearer secret" {
			t.Fatalf("unexpected authorization header")
		}
		var body CreateLinkRequest
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Domain != "menu.example.com" || body.Key != "menu" {
			t.Fatalf("configured defaults were not applied: %+v", body)
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{
			"id":"link_123",
			"domain":"menu.example.com",
			"key":"menu",
			"url":"https://app.example.com/r/test",
			"shortLink":"https://menu.example.com/menu",
			"qrCode":"https://api.dub.co/qr?url=test"
		}`))
	}))
	defer server.Close()

	service, err := New(Config{
		APIKey: "secret", BaseURL: server.URL, Domain: "menu.example.com", LinkKey: "menu",
	})
	if err != nil {
		t.Fatal(err)
	}
	link, err := service.CreateMenuLink(context.Background(), CreateLinkRequest{
		URL: "https://app.example.com/r/test", ExternalID: "restaurant-id",
	})
	if err != nil {
		t.Fatal(err)
	}
	if link.ID != "link_123" || link.ShortURL == "" || link.QRCodeURL == "" {
		t.Fatalf("unexpected link response: %+v", link)
	}
}

func TestRetrieveMenuLinkEncodesLinkID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/links/info" || request.URL.Query().Get("linkId") != "link/123" {
			t.Fatalf("unexpected request URL: %s", request.URL.String())
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{
			"id":"link/123",
			"shortLink":"https://dub.sh/menu",
			"qrCode":"https://api.dub.co/qr?url=test"
		}`))
	}))
	defer server.Close()

	service, err := New(Config{APIKey: "secret", BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.RetrieveMenuLink(context.Background(), "link/123"); err != nil {
		t.Fatal(err)
	}
}
