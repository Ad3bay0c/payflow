// internal/handler/response.go

package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

type envelope struct {
	Success bool      `json:"success"`
	Data    any       `json:"data,omitempty"`
	Error   *apiError `json:"error,omitempty"`
	Meta    *meta     `json:"meta,omitempty"`
	TraceID string    `json:"trace_id,omitempty"`
}

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type meta struct {
	Page       int   `json:"page"`
	PerPage    int   `json:"per_page"`
	TotalCount int64 `json:"total_count"`
	TotalPages int   `json:"total_pages"`
}

func ok(c *gin.Context, data any) {
	c.JSON(http.StatusOK, envelope{
		Success: true,
		Data:    data,
		TraceID: getTraceID(c),
	})
}

func created(c *gin.Context, data any) {
	c.JSON(http.StatusCreated, envelope{
		Success: true,
		Data:    data,
		TraceID: getTraceID(c),
	})
}

func paginated(c *gin.Context, data any, m *meta) {
	c.JSON(http.StatusOK, envelope{
		Success: true,
		Data:    data,
		Meta:    m,
		TraceID: getTraceID(c),
	})
}

func fail(c *gin.Context, status int, code, message string) {
	c.JSON(status, envelope{
		Success: false,
		Error:   &apiError{Code: code, Message: message},
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
