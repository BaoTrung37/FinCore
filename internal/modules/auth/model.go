package auth

import "time"

// User represents a FinCore user account.
type User struct {
	ID           string    `json:"id"`
	Email        string    `json:"email"`
	PasswordHash string    `json:"-"`
	Status       string    `json:"status"`
	CreatedAt    time.Time `json:"created_at"`
}

// RegisterRequest defines the payload for user registration.
type RegisterRequest struct {
	Email    string `json:"email" validate:"required,email"`
	Password string `json:"password" validate:"required,min=8,max=72"`
}

// LoginRequest defines the payload for user login.
type LoginRequest struct {
	Email    string `json:"email" validate:"required,email"`
	Password string `json:"password" validate:"required"`
}

// UserResponse is the public user representation returned by API endpoints.
type UserResponse struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

// AuthResponse is returned after successful registration or login.
type AuthResponse struct {
	AccessToken string       `json:"access_token"`
	User        UserResponse `json:"user"`
}

func toUserResponse(u User) UserResponse {
	return UserResponse{
		ID:        u.ID,
		Email:     u.Email,
		Status:    u.Status,
		CreatedAt: u.CreatedAt,
	}
}
