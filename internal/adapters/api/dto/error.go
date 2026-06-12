package dto

import "github.com/gin-gonic/gin"

// ErrorResponse is the uniform error envelope (F-MVP-11): { code, message, details }.
type ErrorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details any    `json:"details,omitempty"`
}

// Error codes used across the API.
const (
	CodeValidation    = "validation_error"
	CodeUnauthorized  = "unauthorized"
	CodeForbidden     = "forbidden"
	CodeNotFound      = "not_found"
	CodeConflict      = "conflict"
	CodeUnprocessable = "unprocessable_entity"
	CodeInternal      = "internal_error"
)

// Abort writes the error envelope and stops the handler chain.
func Abort(c *gin.Context, status int, code, message string, details ...any) {
	resp := ErrorResponse{Code: code, Message: message}
	if len(details) > 0 {
		resp.Details = details[0]
	}
	c.AbortWithStatusJSON(status, resp)
}
