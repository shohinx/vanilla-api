package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func AuthorizeAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		principal, err := Principal(c)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			c.Abort()
			return
		}

		if !principal.CanAdminister() {
			c.JSON(http.StatusForbidden, gin.H{"error": "forbidden"})
			c.Abort()
			return
		}

		c.Next()
	}
}
