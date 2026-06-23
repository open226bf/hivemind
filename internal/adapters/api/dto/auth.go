package dto

import "time"

type LoginRequest struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required"`
}

type RefreshRequest struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

type TokenResponse struct {
	AccessToken     string    `json:"access_token"`
	RefreshToken    string    `json:"refresh_token"`
	TokenType       string    `json:"token_type"`
	AccessExpiresAt time.Time `json:"access_expires_at"`
}

type MeResponse struct {
	ID      string     `json:"id"`
	Email   string     `json:"email"`
	Role    string     `json:"role"`
	IsAdmin bool       `json:"is_admin"`
	Scopes  []ScopeDTO `json:"scopes"`
	// AclEnforced mirrors HIVEMIND_ACL_ENFORCED so the UI gates on grants only
	// when enforcement is live. In shadow mode (false) the server doesn't filter
	// or block, so the UI must keep its pre-ACL behaviour (no lock-out).
	AclEnforced bool `json:"acl_enforced"`
}

// ScopeDTO is one effective ACL grant carried in /auth/me so the UI can gate
// per-resource actions without a round-trip.
type ScopeDTO struct {
	Type string `json:"type"` // "cluster" | "hive"
	ID   string `json:"id"`
	Verb string `json:"verb"` // "read" | "write" | "manage"
}
