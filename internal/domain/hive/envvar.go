package hive

import (
	"errors"
	"regexp"

	"github.com/google/uuid"
)

var (
	ErrInvalidEnvKey = errors.New("env key must match ^[A-Z_][A-Z0-9_]*$")
	ErrDuplicateKey  = errors.New("duplicate env var key")
)

var envKeyRegex = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)

// EnvVar is a hive-scoped ("global") environment variable. It is applied to
// every service in the hive at deploy time; a service-level variable with the
// same key overrides it. Secret values are encrypted at rest by the repository.
type EnvVar struct {
	ID       uuid.UUID
	HiveID   uuid.UUID
	Key      string
	Value    string
	IsSecret bool
}

func NewEnvVar(hiveID uuid.UUID, key, value string, isSecret bool) (*EnvVar, error) {
	if !envKeyRegex.MatchString(key) {
		return nil, ErrInvalidEnvKey
	}
	return &EnvVar{
		ID:       uuid.New(),
		HiveID:   hiveID,
		Key:      key,
		Value:    value,
		IsSecret: isSecret,
	}, nil
}

// ValidateEnvVars checks each key format and uniqueness.
func ValidateEnvVars(vars []EnvVar) error {
	seen := make(map[string]struct{}, len(vars))
	for _, v := range vars {
		if !envKeyRegex.MatchString(v.Key) {
			return ErrInvalidEnvKey
		}
		if _, exists := seen[v.Key]; exists {
			return ErrDuplicateKey
		}
		seen[v.Key] = struct{}{}
	}
	return nil
}
