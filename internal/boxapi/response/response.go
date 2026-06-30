package response

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

type ErrorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type APIError struct {
	Status  int
	Code    string
	Message string
}

func (e APIError) Error() string {
	return e.Message
}

func JSON(c *gin.Context, status int, data any) {
	c.JSON(status, data)
}

func Error(c *gin.Context, err APIError) {
	c.JSON(err.Status, ErrorResponse{
		Code:    err.Code,
		Message: err.Message,
	})
}

func ErrNotFound(message string) APIError {
	return APIError{Status: http.StatusNotFound, Code: "not_found", Message: message}
}

func ErrBadRequest(message string) APIError {
	return APIError{Status: http.StatusBadRequest, Code: "bad_request", Message: message}
}

func ErrConflict(message string) APIError {
	return APIError{Status: http.StatusConflict, Code: "conflict", Message: message}
}

func ErrInternal(message string) APIError {
	return APIError{Status: http.StatusInternalServerError, Code: "internal", Message: message}
}

func ErrNotImplemented(message string) APIError {
	return APIError{Status: http.StatusNotImplemented, Code: "not_implemented", Message: message}
}
