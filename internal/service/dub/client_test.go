package dub

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestQRCodeRejectsNonDubURL(t *testing.T) {
	client := New("")
	if _, err := client.QRCode(context.Background(), "https://example.com/not-dub.png"); err == nil {
		t.Fatal("expected non-Dub QR URL to be rejected")
	}
}

func TestRetrieveMenuLinkUsesDomainAndKeyAndDecodesQRCode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/links/info" || request.URL.Query().Get("domain") != "dub.sh" || request.URL.Query().Get("key") != "diAI31C" {
			t.Fatalf("unexpected request URL: %s", request.URL.String())
		}
		if request.Header.Get("Authorization") != "Bearer dub_test" {
			t.Fatal("unexpected authorization header")
		}
		response.Header().Set("Content-Type", "application/json")
		fmt.Fprint(response, `{
			"id":"link_1",
			"url":"https://menu.example.com",
			"shortLink":"https://dub.sh/menu",
			"qrCode":"https://api.dub.co/qr?url=https://dub.sh/menu",
			"createdAt":"2026-07-16T12:00:00Z"
		}`)
	}))
	defer server.Close()

	client := New("dub_test")
	client.baseURL = server.URL
	link, err := client.RetrieveMenuLink(context.Background(), "", "dub.sh", "diAI31C")
	if err != nil {
		t.Fatalf("RetrieveMenuLink() returned an error: %v", err)
	}
	if link.ID != "link_1" || link.QRCode != "https://api.dub.co/qr?url=https://dub.sh/menu" {
		t.Fatalf("unexpected link: %+v", link)
	}
}

func TestRetrieveMenuLinkUsesStoredLinkID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Query().Get("linkId") != "link_1" {
			t.Fatalf("unexpected request URL: %s", request.URL.String())
		}
		fmt.Fprint(response, `{"id":"link_1","url":"https://menu.example.com"}`)
	}))
	defer server.Close()

	client := New("dub_test")
	client.baseURL = server.URL
	if _, err := client.RetrieveMenuLink(context.Background(), "link_1", "", ""); err != nil {
		t.Fatalf("RetrieveMenuLink() returned an error: %v", err)
	}
}
