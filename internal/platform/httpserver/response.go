package httpserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/go-playground/validator/v10"
)

const maxBodyBytes = 1 << 20 // 1 MiB

var validate = validator.New()

// ErrRequestTooLarge indicates the request body exceeded maxBodyBytes.
var ErrRequestTooLarge = errors.New("request body too large")

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
func DecodeAndValidate(w http.ResponseWriter, r *http.Request, dst any) error {
	if r.Body == nil {
		return errors.New("request body is empty")
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()

	if err := dec.Decode(dst); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			return ErrRequestTooLarge
		}
		return errors.New("malformed JSON payload")
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return errors.New("request body must contain a single JSON object")
	}

	if err := validate.Struct(dst); err != nil {
		var valErrs validator.ValidationErrors
		if errors.As(err, &valErrs) && len(valErrs) > 0 {
			fields := make([]string, 0, len(valErrs))
			for _, fe := range valErrs {
				fields = append(fields, fmt.Sprintf("%s (%s)", strings.ToLower(fe.Field()), fe.Tag()))
			}
			return fmt.Errorf("validation failed: %s", strings.Join(fields, ", "))
		}
		return errors.New("validation failed")
	}
	return nil
}
