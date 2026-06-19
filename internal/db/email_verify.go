package db

import (
	"context"
	"time"

	"github.com/google/uuid"
)

const verifyEmail = `UPDATE users SET email_verified = true WHERE id = $1`

func (q *Queries) VerifyEmail(ctx context.Context, id uuid.UUID) error {
	_, err := q.db.ExecContext(ctx, verifyEmail, id)
	return err
}

const updateUserEmail = `UPDATE users SET email = $2 WHERE id = $1`

type UpdateUserEmailParams struct {
	ID    uuid.UUID
	Email string
}

func (q *Queries) UpdateUserEmail(ctx context.Context, arg UpdateUserEmailParams) error {
	_, err := q.db.ExecContext(ctx, updateUserEmail, arg.ID, arg.Email)
	return err
}

const incrementTokenVersion = `UPDATE users SET token_version = token_version + 1 WHERE id = $1`

func (q *Queries) IncrementTokenVersion(ctx context.Context, id uuid.UUID) error {
	_, err := q.db.ExecContext(ctx, incrementTokenVersion, id)
	return err
}

const getUserWithTokenVersion = `SELECT id, username, password_hash, email, created_at, email_otp_enabled, email_verified, COALESCE(token_version, 0) as token_version FROM users WHERE id = $1`

type GetUserWithTokenVersionRow struct {
	ID              uuid.UUID
	Username        string
	PasswordHash    string
	Email           string
	CreatedAt       time.Time
	EmailOtpEnabled bool
	EmailVerified   bool
	TokenVersion    int32
}

func (q *Queries) GetUserWithTokenVersion(ctx context.Context, id uuid.UUID) (GetUserWithTokenVersionRow, error) {
	row := q.db.QueryRowContext(ctx, getUserWithTokenVersion, id)
	var i GetUserWithTokenVersionRow
	err := row.Scan(
		&i.ID,
		&i.Username,
		&i.PasswordHash,
		&i.Email,
		&i.CreatedAt,
		&i.EmailOtpEnabled,
		&i.EmailVerified,
		&i.TokenVersion,
	)
	return i, err
}

const getUserByUsernameWithVerified = `SELECT id, username, password_hash, email, created_at, email_otp_enabled, COALESCE(email_verified, false) as email_verified FROM users WHERE username = $1`

type GetUserByUsernameWithVerifiedRow struct {
	ID              uuid.UUID
	Username        string
	PasswordHash    string
	Email           string
	CreatedAt       time.Time
	EmailOtpEnabled bool
	EmailVerified   bool
}

func (q *Queries) GetUserByUsernameWithVerified(ctx context.Context, username string) (GetUserByUsernameWithVerifiedRow, error) {
	row := q.db.QueryRowContext(ctx, getUserByUsernameWithVerified, username)
	var i GetUserByUsernameWithVerifiedRow
	err := row.Scan(
		&i.ID,
		&i.Username,
		&i.PasswordHash,
		&i.Email,
		&i.CreatedAt,
		&i.EmailOtpEnabled,
		&i.EmailVerified,
	)
	return i, err
}

const getUserByIDWithVerified = `SELECT id, username, password_hash, email, created_at, email_otp_enabled, COALESCE(email_verified, false) as email_verified FROM users WHERE id = $1`

type GetUserByIDWithVerifiedRow struct {
	ID              uuid.UUID
	Username        string
	PasswordHash    string
	Email           string
	CreatedAt       time.Time
	EmailOtpEnabled bool
	EmailVerified   bool
}

func (q *Queries) GetUserByIDWithVerified(ctx context.Context, id uuid.UUID) (GetUserByIDWithVerifiedRow, error) {
	row := q.db.QueryRowContext(ctx, getUserByIDWithVerified, id)
	var i GetUserByIDWithVerifiedRow
	err := row.Scan(
		&i.ID,
		&i.Username,
		&i.PasswordHash,
		&i.Email,
		&i.CreatedAt,
		&i.EmailOtpEnabled,
		&i.EmailVerified,
	)
	return i, err
}

const getUserByEmailWithVerified = `SELECT id, username, password_hash, email, created_at, email_otp_enabled, COALESCE(email_verified, false) as email_verified FROM users WHERE email = $1`

type GetUserByEmailWithVerifiedRow struct {
	ID              uuid.UUID
	Username        string
	PasswordHash    string
	Email           string
	CreatedAt       time.Time
	EmailOtpEnabled bool
	EmailVerified   bool
}

func (q *Queries) GetUserByEmailWithVerified(ctx context.Context, email string) (GetUserByEmailWithVerifiedRow, error) {
	row := q.db.QueryRowContext(ctx, getUserByEmailWithVerified, email)
	var i GetUserByEmailWithVerifiedRow
	err := row.Scan(
		&i.ID,
		&i.Username,
		&i.PasswordHash,
		&i.Email,
		&i.CreatedAt,
		&i.EmailOtpEnabled,
		&i.EmailVerified,
	)
	return i, err
}
