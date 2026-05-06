// internal/handler/response.go
//
// Standard response envelope for all PayFlow auth API responses.
// Every response — success or error — uses this shape.
// Consistency means the mobile app has one response format to handle.

package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// envelope is the standard response wrapper.
type envelope struct {
	Success bool      `json:"success"`
	Data    any       `json:"data,omitempty"`
	Error   *apiError `json:"error,omitempty"`
	TraceID string    `json:"trace_id,omitempty"`
}

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// ok sends a 200 response with data.
func ok(c *gin.Context, data any) {
	c.JSON(http.StatusOK, envelope{
		Success: true,
		Data:    data,
		TraceID: getTraceID(c),
	})
}

// created sends a 201 response with data.
func created(c *gin.Context, data any) {
	c.JSON(http.StatusCreated, envelope{
		Success: true,
		Data:    data,
		TraceID: getTraceID(c),
	})
}

// fail sends an error response with the appropriate HTTP status.
func fail(c *gin.Context, status int, code, message string) {
	c.JSON(status, envelope{
		Success: false,
		Error: &apiError{
			Code:    code,
			Message: message,
		},
		TraceID: getTraceID(c),
	})
}

func getTraceID(c *gin.Context) string {
	if id, exists := c.Get("trace_id"); exists {
		if s, ok := id.(string); ok {
			return s
		}
	}
	return ""
}
