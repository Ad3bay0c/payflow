// internal/handler/middleware.go
//
// HTTP middleware for the auth service.
// Middleware runs before every request handler.

package handler

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// TraceID injects a unique trace_id into every request context.
// This ID flows through all log lines for that request —
// making cross-service debugging possible.
func TraceID() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Use the incoming trace ID if provided (from API gateway)
		// Otherwise generate a new one
		traceID := c.GetHeader("X-Trace-ID")
		if traceID == "" {
			traceID = uuid.NewString()
		}

		c.Set("trace_id", traceID)
		c.Header("X-Trace-ID", traceID) // echo it back in the response
		c.Next()
	}
}

// RequireAuth validates the JWT access token on protected routes.
// Extracts claims and stores them in the context for handlers to use.
func (h *AuthHandler) RequireAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			fail(c, http.StatusUnauthorized, "UNAUTHORISED", "missing authorization header")
			c.Abort()
			return
		}

		// Header format: "Bearer <token>"
		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
			fail(c, http.StatusUnauthorized, "UNAUTHORISED", "invalid authorization header format")
			c.Abort()
			return
		}

		claims, err := h.jwtSvc.ValidateToken(c.Request.Context(), parts[1], "access")
		if err != nil {
			fail(c, http.StatusUnauthorized, "UNAUTHORISED", "invalid or expired token")
			c.Abort()
			return
		}

		// Store claims in context — handlers retrieve with getUserID(c)
		c.Set("claims", claims)
		c.Set("access_token", parts[1])
		c.Set("user_id", claims.UserID.String())
		c.Next()
	}
}
