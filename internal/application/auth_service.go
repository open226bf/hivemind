package application

import (
	"context"
	"errors"
	"time"

	"github.com/orange/hivemind/internal/domain/user"
	"github.com/orange/hivemind/internal/ports"
	"github.com/orange/hivemind/pkg/crypto"
	"github.com/orange/hivemind/pkg/domainerrors"
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

// Scoper computes the effective ACL scopes embedded in an access token. Kept as
// a narrow interface so the auth use case doesn't depend on the full AclService.
type Scoper interface {
	ScopesFor(ctx context.Context, u *user.User) ([]ports.Scope, error)
}

type AuthService struct {
	users        ports.UserRepository
	tokens       ports.TokenService
	clock        ports.Clock
	scoper       Scoper
	sentinelHash string
}

// NewAuthService builds the auth use case. scoper may be nil (e.g. in tests or
// before ACLs are wired), in which case tokens carry no scopes.
func NewAuthService(users ports.UserRepository, tokens ports.TokenService, clock ports.Clock, scoper Scoper) *AuthService {
	sentinel, err := crypto.HashPassword("hivemind-sentinel-do-not-use")
	if err != nil {
		panic("auth: cannot compute sentinel hash: " + err.Error())
	}
	return &AuthService{users: users, tokens: tokens, clock: clock, scoper: scoper, sentinelHash: sentinel}
}

// Login authenticates by email/password, enforcing the account lockout policy.
func (s *AuthService) Login(ctx context.Context, email, password string) (*TokenPair, error) {
	u, err := s.users.FindByEmail(ctx, email)
	if errors.Is(err, domainerrors.ErrNotFound) {
		_ = crypto.CheckPassword(s.sentinelHash, password)
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

	return s.issuePair(ctx, u)
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

	return s.issuePair(ctx, u)
}

// Me returns the authenticated user.
func (s *AuthService) Me(ctx context.Context, claims *ports.TokenClaims) (*user.User, error) {
	return s.users.FindByID(ctx, claims.UserID)
}

func (s *AuthService) issuePair(ctx context.Context, u *user.User) (*TokenPair, error) {
	var scopes []ports.Scope
	if s.scoper != nil {
		sc, err := s.scoper.ScopesFor(ctx, u)
		if err != nil {
			return nil, err
		}
		scopes = sc
	}
	access, accessExp, err := s.tokens.GenerateAccessToken(u, scopes)
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
