package dto

import "time"

// ─── Requests ─────────────────────────────────────────────────────────────────

// CreateUserRequest is the body for POST /users.
type CreateUserRequest struct {
	Email    string `json:"email"    binding:"required,email"`
	Password string `json:"password" binding:"required"`
	Role     string `json:"role"     binding:"required" example:"operator"` // admin | operator | viewer
}

// UpdateUserRequest is the body for PUT /users/{id}. All fields are optional;
// absent (null) fields are left unchanged.
type UpdateUserRequest struct {
	Role     *string `json:"role"`
	Active   *bool   `json:"active"`
	Password *string `json:"password"`
}

// ─── Responses ────────────────────────────────────────────────────────────────

// UserResponse is the canonical user representation (never includes the hash).
type UserResponse struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	Role      string    `json:"role"`
	Active    bool      `json:"active"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// UserListResponse wraps a paginated list of users.
type UserListResponse struct {
	Items []UserResponse `json:"items"`
	Total int64          `json:"total"`
	Page  int            `json:"page"`
	Size  int            `json:"size"`
}
