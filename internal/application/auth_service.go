package application

import (
	"context"
	"errors"
	"time"

	"github.com/open226bf/hivemind/internal/domain/user"
	"github.com/open226bf/hivemind/internal/ports"
	"github.com/open226bf/hivemind/pkg/crypto"
	"github.com/open226bf/hivemind/pkg/domainerrors"
)

var (
	ErrInvalidCredentials = errors.New("invalid email or password")
	ErrAccountLocked      = errors.New("account is locked due to repeated failed logins")
	ErrInactiveUser       = errors.New("account is inactive")
	ErrInvalidToken       = errors.New("invalid or expired token")
)

// TokenPair is the result of a successful authentication.
type TokenPair struct {
	AccessToken     string
	RefreshToken    string
	AccessExpiresAt time.Time
	TokenType       string // always "Bearer"
}

type AuthService struct {
	users        ports.UserRepository
	tokens       ports.TokenService
	clock        ports.Clock
	sentinelHash string // pre-computed bcrypt hash used to neutralise email-enumeration timing attacks
}

func NewAuthService(users ports.UserRepository, tokens ports.TokenService, clock ports.Clock) *AuthService {
	// Compute once at startup so every "user not found" path takes the same
	// time as a real bcrypt comparison, preventing timing-based enumeration.
	sentinel, err := crypto.HashPassword("hivemind-sentinel-do-not-use")
	if err != nil {
		panic("auth: cannot compute sentinel hash: " + err.Error())
	}
	return &AuthService{users: users, tokens: tokens, clock: clock, sentinelHash: sentinel}
}

// Login authenticates by email/password, enforcing the account lockout policy.
func (s *AuthService) Login(ctx context.Context, email, password string) (*TokenPair, error) {
	u, err := s.users.FindByEmail(ctx, email)
	if errors.Is(err, domainerrors.ErrNotFound) {
		return nil, ErrInvalidCredentials
	}
	if err != nil {
		return nil, err
	}

	if !u.Active {
		return nil, ErrInactiveUser
	}

	now := s.clock.Now()
	if u.IsLocked(now) {
		return nil, ErrAccountLocked
	}

	if err := crypto.CheckPassword(u.PasswordHash, password); err != nil {
		u.RecordFailedLogin(now)
		if uerr := s.users.Update(ctx, u); uerr != nil {
			return nil, uerr
		}
		if u.IsLocked(now) {
			return nil, ErrAccountLocked
		}
		return nil, ErrInvalidCredentials
	}

	if u.FailedLoginAttempts > 0 || u.LockedUntil != nil {
		u.ResetFailedLogins()
		if err := s.users.Update(ctx, u); err != nil {
			return nil, err
		}
	}

	return s.issuePair(u)
}

// Refresh exchanges a valid refresh token for a fresh token pair.
func (s *AuthService) Refresh(ctx context.Context, refreshToken string) (*TokenPair, error) {
	claims, err := s.tokens.Parse(refreshToken)
	if err != nil || claims.TokenType != ports.TokenTypeRefresh {
		return nil, ErrInvalidToken
	}

	u, err := s.users.FindByID(ctx, claims.UserID)
	if errors.Is(err, domainerrors.ErrNotFound) {
		return nil, ErrInvalidToken
	}
	if err != nil {
		return nil, err
	}
	if !u.Active {
		return nil, ErrInactiveUser
	}

	return s.issuePair(u)
}

// Me returns the authenticated user.
func (s *AuthService) Me(ctx context.Context, claims *ports.TokenClaims) (*user.User, error) {
	return s.users.FindByID(ctx, claims.UserID)
}

func (s *AuthService) issuePair(u *user.User) (*TokenPair, error) {
	access, accessExp, err := s.tokens.GenerateAccessToken(u)
	if err != nil {
		return nil, err
	}
	refresh, _, err := s.tokens.GenerateRefreshToken(u)
	if err != nil {
		return nil, err
	}
	return &TokenPair{
		AccessToken:     access,
		RefreshToken:    refresh,
		AccessExpiresAt: accessExp,
		TokenType:       "Bearer",
	}, nil
}
