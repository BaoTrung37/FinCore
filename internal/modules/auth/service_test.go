package auth_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/BaoTrung37/FinCore/internal/modules/auth"
)

type mockRepo struct {
	usersByEmail map[string]auth.User
	usersByID    map[string]auth.User
}

func newMockRepo() *mockRepo {
	return &mockRepo{
		usersByEmail: make(map[string]auth.User),
		usersByID:    make(map[string]auth.User),
	}
}

func (m *mockRepo) CreateUser(_ context.Context, email, passwordHash string) (auth.User, error) {
	if _, exists := m.usersByEmail[email]; exists {
		return auth.User{}, auth.ErrEmailAlreadyExists
	}
	u := auth.User{
		ID:           "uuid-1",
		Email:        email,
		PasswordHash: passwordHash,
		Status:       "active",
		CreatedAt:    time.Now().UTC(),
	}
	m.usersByEmail[email] = u
	m.usersByID[u.ID] = u
	return u, nil
}

func (m *mockRepo) GetUserByEmail(_ context.Context, email string) (auth.User, error) {
	u, ok := m.usersByEmail[email]
	if !ok {
		return auth.User{}, auth.ErrUserNotFound
	}
	return u, nil
}

func (m *mockRepo) GetUserByID(_ context.Context, id string) (auth.User, error) {
	u, ok := m.usersByID[id]
	if !ok {
		return auth.User{}, auth.ErrUserNotFound
	}
	return u, nil
}

func TestService_Register_SuccessAndDuplicate(t *testing.T) {
	repo := newMockRepo()
	tm := auth.NewTokenManager("12345678901234567890123456789012", time.Hour)
	svc := auth.NewService(repo, tm)

	res, err := svc.Register(context.Background(), "Alice@Example.com", "strongpass123")
	if err != nil {
		t.Fatalf("register failed: %v", err)
	}
	if res.AccessToken == "" || res.User.Email != "alice@example.com" {
		t.Fatalf("unexpected auth response: %+v", res)
	}
	if repo.usersByEmail["alice@example.com"].PasswordHash == "strongpass123" {
		t.Fatal("password must be hashed")
	}

	_, err = svc.Register(context.Background(), "alice@example.com", "strongpass123")
	if !errors.Is(err, auth.ErrEmailAlreadyExists) {
		t.Fatalf("expected ErrEmailAlreadyExists, got %v", err)
	}
}

func TestService_Login_ValidInvalidInactive(t *testing.T) {
	repo := newMockRepo()
	tm := auth.NewTokenManager("12345678901234567890123456789012", time.Hour)
	svc := auth.NewService(repo, tm)

	_, err := svc.Register(context.Background(), "bob@example.com", "strongpass123")
	if err != nil {
		t.Fatalf("setup register failed: %v", err)
	}

	res, err := svc.Login(context.Background(), "bob@example.com", "strongpass123")
	if err != nil || res.AccessToken == "" {
		t.Fatalf("expected login success, got res=%+v err=%v", res, err)
	}

	_, err = svc.Login(context.Background(), "bob@example.com", "wrongpass")
	if !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Fatalf("expected ErrInvalidCredentials, got %v", err)
	}

	u := repo.usersByEmail["bob@example.com"]
	u.Status = "suspended"
	repo.usersByEmail["bob@example.com"] = u
	_, err = svc.Login(context.Background(), "bob@example.com", "strongpass123")
	if !errors.Is(err, auth.ErrUserInactive) {
		t.Fatalf("expected ErrUserInactive, got %v", err)
	}
}

func TestService_Register_PasswordOver72Bytes(t *testing.T) {
	repo := newMockRepo()
	tm := auth.NewTokenManager("12345678901234567890123456789012", time.Hour)
	svc := auth.NewService(repo, tm)

	// 40 UTF-8 2-byte runes = 80 bytes > bcrypt's 72-byte limit.
	longMultiByte := "éééééééééééééééééééééééééééééééééééééééé"
	_, err := svc.Register(context.Background(), "multi@example.com", longMultiByte)
	if !errors.Is(err, auth.ErrPasswordTooLong) {
		t.Fatalf("expected ErrPasswordTooLong, got %v", err)
	}
}

func TestService_GetProfile_InactiveUser(t *testing.T) {
	repo := newMockRepo()
	tm := auth.NewTokenManager("12345678901234567890123456789012", time.Hour)
	svc := auth.NewService(repo, tm)

	res, err := svc.Register(context.Background(), "charlie@example.com", "strongpass123")
	if err != nil {
		t.Fatalf("setup register failed: %v", err)
	}

	u := repo.usersByEmail["charlie@example.com"]
	u.Status = "suspended"
	repo.usersByEmail["charlie@example.com"] = u
	repo.usersByID[u.ID] = u

	_, err = svc.GetProfile(context.Background(), res.User.ID)
	if !errors.Is(err, auth.ErrUserInactive) {
		t.Fatalf("expected ErrUserInactive, got %v", err)
	}
}
