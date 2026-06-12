package auth

import (
	"crypto/ed25519"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/orange/hivemind/internal/domain/user"
	"github.com/orange/hivemind/internal/ports"
)

var ErrInvalidToken = errors.New("invalid token")

// claims is the on-the-wire JWT payload.
type claims struct {
	Email     string          `json:"email"`
	Role      string          `json:"role"`
	TokenType ports.TokenType `json:"typ"`
	jwt.RegisteredClaims
}

// TokenService implements ports.TokenService with EdDSA (Ed25519) signed JWTs.
type TokenService struct {
	privateKey ed25519.PrivateKey
	publicKey  ed25519.PublicKey
	issuer     string
	accessTTL  time.Duration
	refreshTTL time.Duration
}

type Config struct {
	PrivateKey ed25519.PrivateKey
	Issuer     string
	AccessTTL  time.Duration
	RefreshTTL time.Duration
}

func NewTokenService(cfg Config) *TokenService {
	accessTTL := cfg.AccessTTL
	if accessTTL == 0 || accessTTL > 15*time.Minute {
		accessTTL = 15 * time.Minute // F-MVP-01: access token ≤ 15 min
	}
	refreshTTL := cfg.RefreshTTL
	if refreshTTL == 0 || refreshTTL > 7*24*time.Hour {
		refreshTTL = 7 * 24 * time.Hour // F-MVP-01: refresh token ≤ 7 days
	}
	issuer := cfg.Issuer
	if issuer == "" {
		issuer = "hivemind"
	}
	return &TokenService{
		privateKey: cfg.PrivateKey,
		publicKey:  cfg.PrivateKey.Public().(ed25519.PublicKey),
		issuer:     issuer,
		accessTTL:  accessTTL,
		refreshTTL: refreshTTL,
	}
}

func (s *TokenService) GenerateAccessToken(u *user.User) (string, time.Time, error) {
	return s.generate(u, ports.TokenTypeAccess, s.accessTTL)
}

func (s *TokenService) GenerateRefreshToken(u *user.User) (string, time.Time, error) {
	return s.generate(u, ports.TokenTypeRefresh, s.refreshTTL)
}

func (s *TokenService) generate(u *user.User, typ ports.TokenType, ttl time.Duration) (string, time.Time, error) {
	now := time.Now().UTC()
	expiresAt := now.Add(ttl)

	c := claims{
		Email:     u.Email,
		Role:      string(u.Role),
		TokenType: typ,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   u.ID.String(),
			Issuer:    s.issuer,
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(expiresAt),
			ID:        uuid.NewString(),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, c)
	signed, err := token.SignedString(s.privateKey)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("sign token: %w", err)
	}
	return signed, expiresAt, nil
}

func (s *TokenService) Parse(tokenString string) (*ports.TokenClaims, error) {
	var c claims
	parsed, err := jwt.ParseWithClaims(tokenString, &c, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodEd25519); !ok {
			return nil, ErrInvalidToken
		}
		return s.publicKey, nil
	}, jwt.WithIssuer(s.issuer), jwt.WithValidMethods([]string{"EdDSA"}))

	if err != nil || !parsed.Valid {
		return nil, ErrInvalidToken
	}

	userID, err := uuid.Parse(c.Subject)
	if err != nil {
		return nil, ErrInvalidToken
	}

	return &ports.TokenClaims{
		UserID:    userID,
		Email:     c.Email,
		Role:      c.Role,
		TokenType: c.TokenType,
	}, nil
}
