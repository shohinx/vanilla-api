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
	"strings"
	"time"

	"github.com/shohinx/vanilla-api/internal/sdk/models"
)

const (
	defaultBaseURL    = "https://api.dub.co"
	defaultTimeout    = 10 * time.Second
	maxResponseBytes  = 1 << 20
	maxQRCodeBytes    = 3 << 20
	maxErrorBodyBytes = 1_024
)

var ErrNotFound = errors.New("dub link not found")

type httpDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type Client struct {
	token      string
	baseURL    string
	httpClient httpDoer
}

func New(token string) *Client {
	return &Client{
		token:      strings.TrimSpace(token),
		baseURL:    defaultBaseURL,
		httpClient: &http.Client{Timeout: defaultTimeout},
	}
}

func (c *Client) CreateMenuLink(ctx context.Context, destination, domain, key string) (models.Link, error) {
	payload := struct {
		URL      string `json:"url"`
		Comments string `json:"comments"`
		Domain   string `json:"domain,omitempty"`
		Key      string `json:"key,omitempty"`
	}{
		URL:      destination,
		Comments: "Vanilla in-store menu QR code",
		Domain:   domain,
		Key:      key,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return models.Link{}, fmt.Errorf("encode dub link request: %w", err)
	}

	endpoint, err := url.JoinPath(c.baseURL, "links")
	if err != nil {
		return models.Link{}, fmt.Errorf("build dub link endpoint: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return models.Link{}, fmt.Errorf("create dub link request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	request.Header.Set("Content-Type", "application/json")

	responseBody, statusCode, err := c.do(request, maxResponseBytes)
	if err != nil {
		return models.Link{}, fmt.Errorf("create dub link: %w", err)
	}
	if statusCode < http.StatusOK || statusCode >= http.StatusMultipleChoices {
		return models.Link{}, upstreamStatusError(statusCode, responseBody)
	}
	return decodeLink(responseBody)
}

func (c *Client) RetrieveMenuLink(ctx context.Context, linkID, domain, key string) (models.Link, error) {
	endpoint, err := url.Parse(c.baseURL)
	if err != nil {
		return models.Link{}, fmt.Errorf("parse dub base URL: %w", err)
	}
	endpoint.Path, err = url.JoinPath(endpoint.Path, "links", "info")
	if err != nil {
		return models.Link{}, fmt.Errorf("build dub retrieve endpoint: %w", err)
	}

	query := endpoint.Query()
	if linkID != "" {
		query.Set("linkId", linkID)
	} else {
		if domain == "" || key == "" {
			return models.Link{}, errors.New("dub domain and link key are required")
		}
		query.Set("domain", domain)
		query.Set("key", key)
	}
	endpoint.RawQuery = query.Encode()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return models.Link{}, fmt.Errorf("create dub retrieve request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+c.token)

	responseBody, statusCode, err := c.do(request, maxResponseBytes)
	if err != nil {
		return models.Link{}, fmt.Errorf("retrieve dub link: %w", err)
	}
	if statusCode == http.StatusNotFound {
		return models.Link{}, ErrNotFound
	}
	if statusCode < http.StatusOK || statusCode >= http.StatusMultipleChoices {
		return models.Link{}, upstreamStatusError(statusCode, responseBody)
	}
	return decodeLink(responseBody)
}

func decodeLink(responseBody []byte) (models.Link, error) {
	var response struct {
		ID        string `json:"id"`
		ShortLink string `json:"shortLink"`
		QRCode    string `json:"qrCode"`
		URL       string `json:"url"`
		CreatedAt string `json:"createdAt"`
	}
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return models.Link{}, fmt.Errorf("decode dub link response: %w", err)
	}
	if response.ID == "" || response.URL == "" {
		return models.Link{}, errors.New("decode dub link response: id and url are required")
	}

	var createdAt time.Time
	if response.CreatedAt != "" {
		parsed, err := time.Parse(time.RFC3339, response.CreatedAt)
		if err != nil {
			return models.Link{}, fmt.Errorf("decode dub link creation time: %w", err)
		}
		createdAt = parsed
	}
	return models.Link{
		ID:          response.ID,
		ShortLink:   response.ShortLink,
		QRCode:      response.QRCode,
		Destination: response.URL,
		CreatedAt:   createdAt,
	}, nil
}

func (c *Client) QRCode(ctx context.Context, qrCodeURL string) ([]byte, error) {
	parsed, err := url.Parse(qrCodeURL)
	if err != nil {
		return nil, fmt.Errorf("parse dub QR code URL: %w", err)
	}
	if parsed.Scheme != "https" || !strings.EqualFold(parsed.Hostname(), "api.dub.co") || parsed.User != nil {
		return nil, errors.New("invalid dub QR code URL")
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create dub QR code request: %w", err)
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("fetch dub QR code: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		closeErr := response.Body.Close()
		return nil, errors.Join(
			fmt.Errorf("dub QR endpoint returned status %d", response.StatusCode),
			wrapCloseError(closeErr),
		)
	}
	contentType := response.Header.Get("Content-Type")
	mediaType, _, mediaTypeErr := mime.ParseMediaType(contentType)
	if mediaTypeErr != nil || mediaType != "image/png" {
		closeErr := response.Body.Close()
		return nil, errors.Join(
			fmt.Errorf("dub QR endpoint returned content type %q, expected image/png", contentType),
			wrapCloseError(closeErr),
		)
	}
	if response.ContentLength > maxQRCodeBytes {
		closeErr := response.Body.Close()
		return nil, errors.Join(
			fmt.Errorf("dub QR code exceeds %d bytes", maxQRCodeBytes),
			wrapCloseError(closeErr),
		)
	}

	image, err := readAndClose(response.Body, maxQRCodeBytes)
	if err != nil {
		return nil, fmt.Errorf("read dub QR code: %w", err)
	}
	if detected := http.DetectContentType(image); detected != "image/png" {
		return nil, fmt.Errorf("dub QR payload has content type %q, expected image/png", detected)
	}
	return image, nil
}

func (c *Client) do(request *http.Request, limit int64) ([]byte, int, error) {
	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, 0, err
	}
	body, err := readAndClose(response.Body, limit)
	if err != nil {
		return nil, response.StatusCode, err
	}
	return body, response.StatusCode, nil
}

func readAndClose(body io.ReadCloser, limit int64) (contents []byte, err error) {
	defer func() {
		err = errors.Join(err, wrapCloseError(body.Close()))
	}()

	contents, err = io.ReadAll(io.LimitReader(body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(contents)) > limit {
		return nil, fmt.Errorf("response exceeds %d bytes", limit)
	}
	return contents, nil
}

func wrapCloseError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("close response body: %w", err)
}

func upstreamStatusError(statusCode int, responseBody []byte) error {
	message := strings.TrimSpace(string(responseBody))
	if len(message) > maxErrorBodyBytes {
		message = message[:maxErrorBodyBytes] + "…"
	}
	if message == "" {
		return fmt.Errorf("dub returned status %d", statusCode)
	}
	return fmt.Errorf("dub returned status %d: %s", statusCode, message)
}
