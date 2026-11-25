package auth

import (
	"errors"
	"net/http"

	"github.com/BaoTrung37/FinCore/internal/platform/httpserver"
	"github.com/go-chi/chi/v5"
)

// Handler exposes HTTP endpoints for the auth module.
type Handler struct {
	svc *Service
}

// NewHandler constructs an auth Handler.
func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// RegisterRoutes mounts auth routes on the given chi router.
func (h *Handler) RegisterRoutes(r chi.Router, tm *TokenManager) {
	r.Route("/api/v1/auth", func(r chi.Router) {
		r.Post("/register", h.register)
		r.Post("/login", h.login)
		r.With(RequireAuth(tm)).Get("/me", h.me)
	})
}

func (h *Handler) register(w http.ResponseWriter, r *http.Request) {
	var req RegisterRequest
	if err := httpserver.DecodeAndValidate(r, &req); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error())
		return
	}

	res, err := h.svc.Register(r.Context(), req.Email, req.Password)
	if err != nil {
		if errors.Is(err, ErrEmailAlreadyExists) {
			httpserver.WriteError(w, http.StatusConflict, "EMAIL_ALREADY_EXISTS", "email is already registered")
			return
		}
		httpserver.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to register user")
		return
	}

	httpserver.WriteJSON(w, http.StatusCreated, res)
}

func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	var req LoginRequest
	if err := httpserver.DecodeAndValidate(r, &req); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error())
		return
	}

	res, err := h.svc.Login(r.Context(), req.Email, req.Password)
	if err != nil {
		switch {
		case errors.Is(err, ErrInvalidCredentials):
			httpserver.WriteError(w, http.StatusUnauthorized, "INVALID_CREDENTIALS", "invalid email or password")
		case errors.Is(err, ErrUserInactive):
			httpserver.WriteError(w, http.StatusForbidden, "USER_INACTIVE", "user account is not active")
		default:
			httpserver.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to login")
		}
		return
	}

	httpserver.WriteJSON(w, http.StatusOK, res)
}

func (h *Handler) me(w http.ResponseWriter, r *http.Request) {
	userID, ok := UserIDFromContext(r.Context())
	if !ok {
		httpserver.WriteError(w, http.StatusUnauthorized, "UNAUTHORIZED", "missing authenticated user")
		return
	}

	profile, err := h.svc.GetProfile(r.Context(), userID)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			httpserver.WriteError(w, http.StatusNotFound, "USER_NOT_FOUND", "user not found")
			return
		}
		httpserver.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to load profile")
		return
	}

	httpserver.WriteJSON(w, http.StatusOK, profile)
}
