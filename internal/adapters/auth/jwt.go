package auth

import (
	"crypto/ed25519"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/open226bf/hivemind/internal/domain/acl"
	"github.com/open226bf/hivemind/internal/domain/user"
	"github.com/open226bf/hivemind/internal/ports"
)

var ErrInvalidToken = errors.New("invalid token")

// scopeClaim is the compact wire form of a ports.Scope (t/i/v keys keep the
// token small).
type scopeClaim struct {
	Type string `json:"t"`
	ID   string `json:"i"`
	Verb string `json:"v"`
}

// claims is the on-the-wire JWT payload.
type claims struct {
	Email     string          `json:"email"`
	Role      string          `json:"role"`
	TokenType ports.TokenType `json:"typ"`
	TokenVer  int             `json:"tv,omitempty"`
	Scopes    []scopeClaim    `json:"scp,omitempty"`
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

func (s *TokenService) GenerateAccessToken(u *user.User, scopes []ports.Scope) (string, time.Time, error) {
	return s.generate(u, ports.TokenTypeAccess, s.accessTTL, scopes)
}

func (s *TokenService) GenerateRefreshToken(u *user.User) (string, time.Time, error) {
	// Refresh tokens stay light: scopes are recomputed on every refresh.
	return s.generate(u, ports.TokenTypeRefresh, s.refreshTTL, nil)
}

func (s *TokenService) generate(u *user.User, typ ports.TokenType, ttl time.Duration, scopes []ports.Scope) (string, time.Time, error) {
	now := time.Now().UTC()
	expiresAt := now.Add(ttl)

	c := claims{
		Email:     u.Email,
		Role:      string(u.Role),
		TokenType: typ,
		TokenVer:  u.TokenVersion,
		Scopes:    toScopeClaims(scopes),
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
		TokenVer:  c.TokenVer,
		Scopes:    fromScopeClaims(c.Scopes),
	}, nil
}

// toScopeClaims encodes ports.Scope values into their compact wire form.
func toScopeClaims(scopes []ports.Scope) []scopeClaim {
	if len(scopes) == 0 {
		return nil
	}
	out := make([]scopeClaim, 0, len(scopes))
	for _, s := range scopes {
		out = append(out, scopeClaim{Type: string(s.Type), ID: s.ID.String(), Verb: string(s.Verb)})
	}
	return out
}

// fromScopeClaims decodes wire scopes back into ports.Scope, dropping any
// entry with an unparseable id (defensive: a malformed scope grants nothing).
func fromScopeClaims(scopes []scopeClaim) []ports.Scope {
	if len(scopes) == 0 {
		return nil
	}
	out := make([]ports.Scope, 0, len(scopes))
	for _, s := range scopes {
		id, err := uuid.Parse(s.ID)
		if err != nil {
			continue
		}
		out = append(out, ports.Scope{Type: acl.ResourceType(s.Type), ID: id, Verb: acl.Verb(s.Verb)})
	}
	return out
}
