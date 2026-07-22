package middleware

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/shohinx/vanilla-api/internal/sdk/models"
)

type Membership = models.Membership

type MembershipResolver interface {
	ResolveMembership(context.Context, string, string) (Membership, error)
}

func RequireMembership(resolver MembershipResolver) gin.HandlerFunc {
	return func(c *gin.Context) {
		if resolver == nil {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "admin_api_unavailable"})
			return
		}
		principal, err := Principal(c)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}
		membership, err := resolver.ResolveMembership(c.Request.Context(), principal.ID, c.Param("organizationID"))
		if err != nil {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "forbidden"})
			return
		}
		c.Set("user_id", membership.UserID)
		c.Set("role", membership.Role)
		c.Next()
	}
}
