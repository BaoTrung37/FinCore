package httpserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/go-playground/validator/v10"
)

const maxBodyBytes = 1 << 20 // 1 MiB

var validate = validator.New()

// ErrorDetail represents a structured API error payload.
type ErrorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// ErrorResponse wraps ErrorDetail in an "error" envelope.
type ErrorResponse struct {
	Error ErrorDetail `json:"error"`
}

// WriteJSON encodes data as JSON with the given HTTP status code.
func WriteJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

// WriteError writes a standardized JSON error response.
func WriteError(w http.ResponseWriter, status int, code, message string) {
	WriteJSON(w, status, ErrorResponse{
		Error: ErrorDetail{
			Code:    code,
			Message: message,
		},
	})
}

// DecodeAndValidate decodes a JSON request body into dst and runs struct validation.
func DecodeAndValidate(r *http.Request, dst any) error {
	if r.Body == nil {
		return errors.New("request body is empty")
	}

	limited := io.LimitReader(r.Body, maxBodyBytes)
	dec := json.NewDecoder(limited)
	dec.DisallowUnknownFields()

	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("invalid json: %w", err)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return errors.New("request body must contain a single JSON object")
	}

	if err := validate.Struct(dst); err != nil {
		return fmt.Errorf("validation failed: %w", err)
	}
	return nil
}
