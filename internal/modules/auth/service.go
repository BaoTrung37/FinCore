package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

var (
	ErrInvalidCredentials = errors.New("invalid email or password")
	ErrUserInactive       = errors.New("user account is not active")
	ErrPasswordTooLong    = errors.New("password must not exceed 72 bytes")
)

// Service implements authentication business logic.
type Service struct {
	repo Repository
	tm   *TokenManager
}

// NewService constructs an auth Service.
func NewService(repo Repository, tm *TokenManager) *Service {
	return &Service{repo: repo, tm: tm}
}

// Register creates a new user with a bcrypt-hashed password and returns a JWT.
func (s *Service) Register(ctx context.Context, email, password string) (AuthResponse, error) {
	normalizedEmail := normalizeEmail(email)
	if len(password) > 72 {
		return AuthResponse{}, ErrPasswordTooLong
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return AuthResponse{}, fmt.Errorf("hash password: %w", err)
	}

	user, err := s.repo.CreateUser(ctx, normalizedEmail, string(hash))
	if err != nil {
		return AuthResponse{}, err
	}

	token, err := s.tm.Generate(user.ID, user.Email)
	if err != nil {
		return AuthResponse{}, err
	}

	return AuthResponse{
		AccessToken: token,
		User:        toUserResponse(user),
	}, nil
}

// Login verifies user credentials and returns a JWT if the account is active.
func (s *Service) Login(ctx context.Context, email, password string) (AuthResponse, error) {
	normalizedEmail := normalizeEmail(email)

	user, err := s.repo.GetUserByEmail(ctx, normalizedEmail)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			return AuthResponse{}, ErrInvalidCredentials
		}
		return AuthResponse{}, err
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		return AuthResponse{}, ErrInvalidCredentials
	}

	if user.Status != "active" {
		return AuthResponse{}, ErrUserInactive
	}

	token, err := s.tm.Generate(user.ID, user.Email)
	if err != nil {
		return AuthResponse{}, err
	}

	return AuthResponse{
		AccessToken: token,
		User:        toUserResponse(user),
	}, nil
}

// GetProfile returns the public profile for the given user ID.
func (s *Service) GetProfile(ctx context.Context, userID string) (UserResponse, error) {
	user, err := s.repo.GetUserByID(ctx, userID)
	if err != nil {
		return UserResponse{}, err
	}
	if user.Status != "active" {
		return UserResponse{}, ErrUserInactive
	}
	return toUserResponse(user), nil
}

func normalizeEmail(email string) string {
	return strings.TrimSpace(strings.ToLower(email))
}
