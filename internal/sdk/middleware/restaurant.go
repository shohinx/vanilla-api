package middleware

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
)

type RestaurantAuthorizer interface {
	RestaurantBelongsToOrganization(context.Context, string, string) (bool, error)
}

func RequireRestaurant(authorizer RestaurantAuthorizer) gin.HandlerFunc {
	return func(c *gin.Context) {
		if authorizer == nil {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "admin_api_unavailable"})
			return
		}
		belongs, err := authorizer.RestaurantBelongsToOrganization(
			c.Request.Context(), c.Param("restaurantID"), c.Param("organizationID"),
		)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "database_error"})
			return
		}
		if !belongs {
			c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": "restaurant_not_found"})
			return
		}
		c.Next()
	}
}
