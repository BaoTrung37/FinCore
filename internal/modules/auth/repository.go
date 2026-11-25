package auth

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrEmailAlreadyExists = errors.New("email already exists")
	ErrUserNotFound       = errors.New("user not found")
)

// Repository defines persistence operations for users.
type Repository interface {
	CreateUser(ctx context.Context, email, passwordHash string) (User, error)
	GetUserByEmail(ctx context.Context, email string) (User, error)
	GetUserByID(ctx context.Context, id string) (User, error)
}

// PostgresRepository implements Repository using pgxpool.
type PostgresRepository struct {
	pool *pgxpool.Pool
}

// NewPostgresRepository constructs a PostgresRepository.
func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

func (r *PostgresRepository) CreateUser(ctx context.Context, email, passwordHash string) (User, error) {
	const query = `
		INSERT INTO users (email, password_hash)
		VALUES ($1, $2)
		RETURNING id::text, email, password_hash, status, created_at
	`
	var u User
	err := r.pool.QueryRow(ctx, query, email, passwordHash).Scan(
		&u.ID, &u.Email, &u.PasswordHash, &u.Status, &u.CreatedAt,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return User{}, ErrEmailAlreadyExists
		}
		return User{}, fmt.Errorf("insert user: %w", err)
	}
	return u, nil
}

func (r *PostgresRepository) GetUserByEmail(ctx context.Context, email string) (User, error) {
	const query = `
		SELECT id::text, email, password_hash, status, created_at
		FROM users
		WHERE email = $1
	`
	return r.scanUser(ctx, query, email)
}

func (r *PostgresRepository) GetUserByID(ctx context.Context, id string) (User, error) {
	const query = `
		SELECT id::text, email, password_hash, status, created_at
		FROM users
		WHERE id = $1::uuid
	`
	return r.scanUser(ctx, query, id)
}

func (r *PostgresRepository) scanUser(ctx context.Context, query string, arg any) (User, error) {
	var u User
	err := r.pool.QueryRow(ctx, query, arg).Scan(
		&u.ID, &u.Email, &u.PasswordHash, &u.Status, &u.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return User{}, ErrUserNotFound
		}
		return User{}, fmt.Errorf("query user: %w", err)
	}
	return u, nil
}
