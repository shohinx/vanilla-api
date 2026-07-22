package server

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/shohinx/vanilla-api/internal/services/dub"
)

type menuQRResponse struct {
	ShortURL       string `json:"short_url"`
	QRCodeURL      string `json:"qr_code_url"`
	DestinationURL string `json:"destination_url"`
}

// GetMenuQR returns the existing restaurant-wide Dub link and its generated QR
// image URL. Dub remains the source of truth; this endpoint stores no link or
// analytics data locally.
func (s *Server) GetMenuQR(c *gin.Context) {
	if s.menuLinks == nil || s.dubDomain == "" || s.dubLinkKey == "" {
		writeError(c, http.StatusServiceUnavailable, "menu_qr_not_configured", nil)
		return
	}

	link, err := s.menuLinks.RetrieveMenuLinkByKey(
		c.Request.Context(),
		s.dubDomain,
		s.dubLinkKey,
	)
	if errors.Is(err, dub.ErrNotFound) {
		writeError(c, http.StatusNotFound, "menu_qr_not_found", nil)
		return
	}
	if err != nil {
		writeError(c, http.StatusBadGateway, "dub_unavailable", nil)
		return
	}

	c.JSON(http.StatusOK, menuQRResponse{
		ShortURL:       link.ShortURL,
		QRCodeURL:      link.QRCodeURL,
		DestinationURL: link.URL,
	})
}
