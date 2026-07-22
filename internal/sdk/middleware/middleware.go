// Package middleware provides HTTP middleware for authentication and authorization.
package middleware

import (
	"github.com/gin-gonic/gin"
	"github.com/shohinx/vanilla-api/internal/sdk/errs"
	"github.com/shohinx/vanilla-api/internal/sdk/models"
)

const principalKey = "principal"

// SetPrincipal stores the authenticated principal in the request context.
func SetPrincipal(c *gin.Context, principal models.Principal) {
	c.Set(principalKey, principal)
}

func Principal(c *gin.Context) (models.Principal, error) {
	value, ok := c.Get(principalKey)
	if !ok {
		return models.Principal{}, errs.ErrUnauthorized
	}
	principal, ok := value.(models.Principal)
	if !ok || principal.ID == "" {
		return models.Principal{}, errs.ErrUnauthorized
	}
	return principal, nil
}

func GetUserID(c *gin.Context) (string, error) {
	principal, err := Principal(c)
	if err != nil {
		return "", err
	}
	return principal.ID, nil
}
