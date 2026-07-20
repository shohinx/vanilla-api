package server

import (
	"errors"
	"net/http"
	"testing"
)

func TestApplicationClosesResourcesExactlyOnce(t *testing.T) {
	closeCalls := 0
	closeCause := errors.New("close failed")
	application := &Application{
		Server: &http.Server{},
		closeFunc: func() error {
			closeCalls++
			return closeCause
		},
	}

	firstErr := application.Close()
	secondErr := application.Close()
	if closeCalls != 1 {
		t.Fatalf("expected one resource close, got %d", closeCalls)
	}
	if !errors.Is(firstErr, closeCause) || !errors.Is(secondErr, closeCause) {
		t.Fatalf("expected close cause to remain unwrap-able: first=%v second=%v", firstErr, secondErr)
	}
}

func TestSplitCSVUsesTrimmedNonEmptyValues(t *testing.T) {
	values := splitCSV(" https://one.example, ,https://two.example ")
	if len(values) != 2 || values[0] != "https://one.example" || values[1] != "https://two.example" {
		t.Fatalf("unexpected values: %v", values)
	}
}

func TestConfigValidationRejectsPartialOrUnsafeValues(t *testing.T) {
	for name, config := range map[string]Config{
		"partial Dub configuration": {DubAPIKey: "token"},
		"Dub domain with a port":    {DubAPIKey: "token", DubDomain: "dub.sh:443", DubLinkKey: "menu"},
		"relative menu URL":         {MenuAppURL: "/menu"},
		"invalid public URL scheme": {PublicBaseURL: "ftp://api.example.com"},
		"invalid allowed origin":    {AllowedOrigins: []string{"api.example.com"}},
	} {
		t.Run(name, func(t *testing.T) {
			if err := config.validate(); err == nil {
				t.Fatalf("expected config to be rejected: %+v", config)
			}
		})
	}

	valid := Config{
		MenuAppURL:     "https://menu.example.com",
		PublicBaseURL:  "https://api.example.com",
		AllowedOrigins: []string{"https://menu.example.com"},
		DubAPIKey:      "token",
		DubDomain:      "dub.sh",
		DubLinkKey:     "menu",
	}
	if err := valid.validate(); err != nil {
		t.Fatalf("expected valid config, got %v", err)
	}
}
