package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"golang.org/x/crypto/bcrypt"
)

type Role string

const (
	Viewer   Role = "viewer"
	Operator Role = "operator"
	Admin    Role = "admin"
)

var (
	ErrForbidden                = errors.New("forbidden")
	ErrReauthenticationRequired = errors.New("recent reauthentication required")
)

type Actor struct {
	UserID            int64
	Username          string
	Role              Role
	SessionID         string
	ReauthenticatedAt time.Time
}

type actorContextKey struct{}

// WithActor stores the authenticated session actor on a request context.
func WithActor(ctx context.Context, actor Actor) context.Context {
	return context.WithValue(ctx, actorContextKey{}, actor)
}

// ActorFromContext returns the authenticated session actor, if one is present.
func ActorFromContext(ctx context.Context) (Actor, bool) {
	actor, ok := ctx.Value(actorContextKey{}).(Actor)
	return actor, ok
}

// Require checks whether actor has at least the requested role.
func Require(actor Actor, required Role) error {
	if roleRank(actor.Role) < roleRank(required) || roleRank(required) == 0 {
		return fmt.Errorf("%w: requires %s", ErrForbidden, required)
	}
	return nil
}

// RequireRecentReauth rejects sessions without a non-expired reauthentication.
func RequireRecentReauth(actor Actor, maxAge time.Duration) error {
	now := time.Now()
	if maxAge <= 0 || actor.ReauthenticatedAt.IsZero() || actor.ReauthenticatedAt.After(now) || now.Sub(actor.ReauthenticatedAt) > maxAge {
		return ErrReauthenticationRequired
	}
	return nil
}

func roleRank(role Role) int {
	switch role {
	case Viewer:
		return 1
	case Operator:
		return 2
	case Admin:
		return 3
	default:
		return 0
	}
}

func HashPassword(pw string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	return string(b), err
}

func CheckPassword(hash, pw string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw)) == nil
}

func NewSession(d *sql.DB, userID int64, ttl time.Duration) (string, error) {
	token := randomToken()
	_, err := d.Exec(`INSERT INTO sessions(token,session_id,user_id,created_at,expires_at) VALUES(?,?,?,?,?)`,
		token, sessionReference(token), userID, time.Now().Unix(), time.Now().Add(ttl).Unix())
	return token, err
}

func randomToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func sessionReference(token string) string {
	digest := sha256.Sum256([]byte(token))
	return hex.EncodeToString(digest[:])
}
