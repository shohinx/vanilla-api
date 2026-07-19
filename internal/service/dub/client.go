package dub

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/shohinx/vanilla-api/internal/sdk/models"
)

type Service interface {
	CreateMenuLink(context.Context, string, string, string) (models.Link, error)
	RetrieveMenuLink(context.Context, string, string, string) (models.Link, error)
	QRCode(context.Context, string) ([]byte, error)
}

var ErrNotFound = errors.New("Dub link not found")

type Client struct {
	token      string
	baseURL    string
	httpClient *http.Client
}

func New(token string) *Client {
	return &Client{
		token:      token,
		baseURL:    "https://api.dub.co",
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

func (c *Client) CreateMenuLink(ctx context.Context, destination, domain, key string) (models.Link, error) {
	payload := map[string]any{
		"url":      destination,
		"comments": "Vanilla in-store menu QR code",
	}
	if domain != "" {
		payload["domain"] = domain
	}
	if key != "" {
		payload["key"] = key
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return models.Link{}, fmt.Errorf("encode Dub request: %w", err)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/links", bytes.NewReader(body))
	if err != nil {
		return models.Link{}, fmt.Errorf("create Dub request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	request.Header.Set("Content-Type", "application/json")

	response, err := c.httpClient.Do(request)
	if err != nil {
		return models.Link{}, fmt.Errorf("call Dub: %w", err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return models.Link{}, fmt.Errorf("read Dub response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return models.Link{}, fmt.Errorf("Dub returned status %d: %s", response.StatusCode, strings.TrimSpace(string(responseBody)))
	}

	link, err := decodeLink(responseBody)
	if err != nil {
		return models.Link{}, err
	}
	return link, nil
}

func (c *Client) RetrieveMenuLink(ctx context.Context, linkID, domain, key string) (models.Link, error) {
	endpoint, _ := url.Parse(c.baseURL + "/links/info")
	query := endpoint.Query()
	if linkID != "" {
		query.Set("linkId", linkID)
	} else {
		if domain == "" || key == "" {
			return models.Link{}, fmt.Errorf("Dub domain and link key are required")
		}
		query.Set("domain", domain)
		query.Set("key", key)
	}
	endpoint.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return models.Link{}, fmt.Errorf("create Dub retrieve request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	response, err := c.httpClient.Do(request)
	if err != nil {
		return models.Link{}, fmt.Errorf("retrieve Dub link: %w", err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return models.Link{}, fmt.Errorf("read Dub response: %w", err)
	}
	if response.StatusCode == http.StatusNotFound {
		return models.Link{}, ErrNotFound
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return models.Link{}, fmt.Errorf("Dub returned status %d: %s", response.StatusCode, strings.TrimSpace(string(responseBody)))
	}
	return decodeLink(responseBody)
}

func decodeLink(responseBody []byte) (models.Link, error) {
	var result struct {
		ID        string `json:"id"`
		ShortLink string `json:"shortLink"`
		QRCode    string `json:"qrCode"`
		URL       string `json:"url"`
		CreatedAt string `json:"createdAt"`
	}
	if err := json.Unmarshal(responseBody, &result); err != nil {
		return models.Link{}, fmt.Errorf("decode Dub response: %w", err)
	}
	createdAt, _ := time.Parse(time.RFC3339, result.CreatedAt)
	return models.Link{ID: result.ID, ShortLink: result.ShortLink, QRCode: result.QRCode, Destination: result.URL, CreatedAt: createdAt}, nil
}

func (c *Client) QRCode(ctx context.Context, qrCodeURL string) ([]byte, error) {
	parsed, err := url.Parse(qrCodeURL)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() != "api.dub.co" {
		return nil, fmt.Errorf("invalid Dub QR code URL")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, qrCodeURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create QR code request: %w", err)
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("fetch QR code: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Dub QR endpoint returned status %d", response.StatusCode)
	}
	if !strings.HasPrefix(response.Header.Get("Content-Type"), "image/png") {
		return nil, fmt.Errorf("Dub QR endpoint did not return a PNG")
	}
	image, err := io.ReadAll(io.LimitReader(response.Body, 3<<20))
	if err != nil {
		return nil, fmt.Errorf("read QR code: %w", err)
	}
	return image, nil
}
