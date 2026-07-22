package dub

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	defaultBaseURL = "https://api.dub.co"
	defaultTimeout = 10 * time.Second
	maxQRCodeBytes = 3 << 20
)

var ErrNotFound = errors.New("dub link not found")

type Config struct {
	APIKey  string
	Domain  string
	LinkKey string
	BaseURL string
	Timeout time.Duration
}

type CreateLinkRequest struct {
	URL        string `json:"url"`
	Domain     string `json:"domain,omitempty"`
	Key        string `json:"key,omitempty"`
	ExternalID string `json:"externalId,omitempty"`
	TenantID   string `json:"tenantId,omitempty"`
	Comments   string `json:"comments,omitempty"`
}

type Link struct {
	ID         string `json:"id"`
	Domain     string `json:"domain"`
	Key        string `json:"key"`
	URL        string `json:"url"`
	ExternalID string `json:"externalId"`
	TenantID   string `json:"tenantId"`
	ShortURL   string `json:"shortLink"`
	QRCodeURL  string `json:"qrCode"`
	Archived   bool   `json:"archived"`
	CreatedAt  string `json:"createdAt"`
	UpdatedAt  string `json:"updatedAt"`
}

type APIError struct {
	StatusCode int
	Code       string
	Message    string
}

func (e *APIError) Error() string {
	if e.Code == "" {
		return fmt.Sprintf("Dub API returned HTTP %d: %s", e.StatusCode, e.Message)
	}
	return fmt.Sprintf("Dub API returned HTTP %d (%s): %s", e.StatusCode, e.Code, e.Message)
}

type Service struct {
	apiKey     string
	domain     string
	linkKey    string
	baseURL    string
	httpClient *http.Client
}

func ConfigFromEnv() Config {
	return Config{
		APIKey:  os.Getenv("DUB_API_KEY"),
		Domain:  os.Getenv("DUB_DOMAIN"),
		LinkKey: os.Getenv("DUB_LINK_KEY"),
	}
}

func New(config Config) (*Service, error) {
	config.APIKey = strings.TrimSpace(config.APIKey)
	config.Domain = strings.TrimSpace(config.Domain)
	config.LinkKey = strings.Trim(strings.TrimSpace(config.LinkKey), "/")
	config.BaseURL = strings.TrimRight(strings.TrimSpace(config.BaseURL), "/")
	if config.APIKey == "" {
		return nil, errors.New("DUB_API_KEY is required")
	}
	if config.BaseURL == "" {
		config.BaseURL = defaultBaseURL
	}
	parsedBaseURL, err := url.Parse(config.BaseURL)
	if err != nil || parsedBaseURL.Scheme == "" || parsedBaseURL.Host == "" {
		return nil, errors.New("Dub base URL must be an absolute URL")
	}
	if config.BaseURL == defaultBaseURL && parsedBaseURL.Scheme != "https" {
		return nil, errors.New("Dub API must use HTTPS")
	}
	if config.Timeout <= 0 {
		config.Timeout = defaultTimeout
	}
	return &Service{
		apiKey:     config.APIKey,
		domain:     config.Domain,
		linkKey:    config.LinkKey,
		baseURL:    config.BaseURL,
		httpClient: &http.Client{Timeout: config.Timeout},
	}, nil
}

func (s *Service) CreateMenuLink(ctx context.Context, request CreateLinkRequest) (Link, error) {
	if strings.TrimSpace(request.URL) == "" {
		return Link{}, errors.New("Dub destination URL is required")
	}
	destination, err := url.ParseRequestURI(request.URL)
	if err != nil || destination.Scheme == "" || destination.Host == "" {
		return Link{}, errors.New("Dub destination URL must be absolute")
	}
	if request.Domain == "" {
		request.Domain = s.domain
	}
	if request.Key == "" {
		request.Key = s.linkKey
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return Link{}, fmt.Errorf("encode Dub link request: %w", err)
	}
	return s.do(ctx, http.MethodPost, "/links", bytes.NewReader(payload))
}

func (s *Service) RetrieveMenuLink(ctx context.Context, linkID string) (Link, error) {
	if strings.TrimSpace(linkID) == "" {
		return Link{}, errors.New("Dub link ID is required")
	}
	query := url.Values{"linkId": []string{linkID}}
	return s.do(ctx, http.MethodGet, "/links/info?"+query.Encode(), nil)
}

func (s *Service) RetrieveMenuLinkByKey(ctx context.Context, domain, key string) (Link, error) {
	domain = strings.TrimSpace(domain)
	key = strings.Trim(strings.TrimSpace(key), "/")
	if domain == "" || key == "" {
		return Link{}, errors.New("Dub domain and link key are required")
	}
	query := url.Values{"domain": []string{domain}, "key": []string{key}}
	return s.do(ctx, http.MethodGet, "/links/info?"+query.Encode(), nil)
}

func (s *Service) QRCode(ctx context.Context, qrCodeURL string) ([]byte, error) {
	return fetchQRCode(ctx, s.httpClient, qrCodeURL)
}

func FetchQRCode(ctx context.Context, qrCodeURL string) ([]byte, error) {
	return fetchQRCode(ctx, &http.Client{Timeout: defaultTimeout}, qrCodeURL)
}

func fetchQRCode(ctx context.Context, client *http.Client, qrCodeURL string) ([]byte, error) {
	parsed, err := url.Parse(qrCodeURL)
	if err != nil {
		return nil, fmt.Errorf("parse Dub QR code URL: %w", err)
	}
	if parsed.Scheme != "https" || !strings.EqualFold(parsed.Hostname(), "api.dub.co") || parsed.User != nil {
		return nil, errors.New("invalid Dub QR code URL")
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create Dub QR code request: %w", err)
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("fetch Dub QR code: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Dub QR endpoint returned status %d", response.StatusCode)
	}
	mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || mediaType != "image/png" {
		return nil, fmt.Errorf("Dub QR endpoint returned content type %q, expected image/png", response.Header.Get("Content-Type"))
	}
	if response.ContentLength > maxQRCodeBytes {
		return nil, fmt.Errorf("Dub QR code exceeds %d bytes", maxQRCodeBytes)
	}
	image, err := io.ReadAll(io.LimitReader(response.Body, maxQRCodeBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read Dub QR code: %w", err)
	}
	if len(image) > maxQRCodeBytes {
		return nil, fmt.Errorf("Dub QR code exceeds %d bytes", maxQRCodeBytes)
	}
	if detected := http.DetectContentType(image); detected != "image/png" {
		return nil, fmt.Errorf("Dub QR payload has content type %q, expected image/png", detected)
	}
	return image, nil
}

func (s *Service) do(ctx context.Context, method, path string, body io.Reader) (Link, error) {
	request, err := http.NewRequestWithContext(ctx, method, s.baseURL+path, body)
	if err != nil {
		return Link{}, fmt.Errorf("create Dub request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+s.apiKey)
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := s.httpClient.Do(request)
	if err != nil {
		return Link{}, fmt.Errorf("call Dub API: %w", err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return Link{}, fmt.Errorf("read Dub response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var errorBody struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
			Code    string `json:"code"`
			Message string `json:"message"`
		}
		_ = json.Unmarshal(responseBody, &errorBody)
		code := errorBody.Code
		message := errorBody.Message
		if errorBody.Error.Code != "" {
			code = errorBody.Error.Code
		}
		if errorBody.Error.Message != "" {
			message = errorBody.Error.Message
		}
		if message == "" {
			message = http.StatusText(response.StatusCode)
		}
		apiError := &APIError{StatusCode: response.StatusCode, Code: code, Message: message}
		if response.StatusCode == http.StatusNotFound {
			return Link{}, fmt.Errorf("%w: %w", ErrNotFound, apiError)
		}
		return Link{}, apiError
	}
	var link Link
	if err := json.Unmarshal(responseBody, &link); err != nil {
		return Link{}, fmt.Errorf("decode Dub response: %w", err)
	}
	if link.ID == "" || link.ShortURL == "" || link.QRCodeURL == "" {
		return Link{}, errors.New("Dub response is missing link identifiers or URLs")
	}
	return link, nil
}
