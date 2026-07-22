package server

import (
	"net/http"
	"os"
	"runtime"

	"github.com/gin-gonic/gin"
)

var osHostname = os.Hostname

// HandleReadiness reports whether this instance can serve traffic. A non-200
// status takes the instance out of rotation, so the DB being unreachable must
// surface as 503 — not as a 200 with "down" in the body.
func (s *Server) HandleReadiness(c *gin.Context) {
	stats := s.repository.Health()

	status := http.StatusOK
	if stats["status"] != "up" {
		status = http.StatusServiceUnavailable
	}

	c.JSON(status, stats)
}

func (s *Server) HandleLiveness(c *gin.Context) {
	host, err := osHostname()
	if err != nil || host == "" {
		host = "unavailable"
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"status":     "down",
			"host":       host,
			"gomaxprocs": runtime.GOMAXPROCS(0),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":     "up",
		"host":       host,
		"gomaxprocs": runtime.GOMAXPROCS(0),
	})
}
