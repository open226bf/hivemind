package config

import (
	"errors"
	"regexp"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

const maxContentSize = 500 * 1024 // 500 KB — Swarm limit

var (
	ErrConfigInUse     = errors.New("config is attached to one or more services")
	ErrContentTooLarge = errors.New("config content exceeds 500 KB")
	ErrInvalidUTF8     = errors.New("config content must be valid UTF-8")
	ErrInvalidName     = errors.New("config name must match ^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,62}$")
)

var nameRegex = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,62}$`)

type Config struct {
	ID             uuid.UUID
	Name           string
	TargetPath     string
	CurrentVersion int
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type ConfigVersion struct {
	ID            uuid.UUID
	ConfigID      uuid.UUID
	Version       int
	Content       []byte
	SwarmConfigID string
	Comment       string
	CreatedBy     uuid.UUID
	CreatedAt     time.Time
}

func New(name, targetPath string, content []byte, comment string, createdBy uuid.UUID) (*Config, *ConfigVersion, error) {
	if !nameRegex.MatchString(name) {
		return nil, nil, ErrInvalidName
	}
	if err := validateContent(content); err != nil {
		return nil, nil, err
	}
	id := uuid.New()
	c := &Config{
		ID:             id,
		Name:           name,
		TargetPath:     targetPath,
		CurrentVersion: 1,
		CreatedAt:      time.Now().UTC(),
		UpdatedAt:      time.Now().UTC(),
	}
	v := &ConfigVersion{
		ID:        uuid.New(),
		ConfigID:  id,
		Version:   1,
		Content:   content,
		Comment:   comment,
		CreatedBy: createdBy,
		CreatedAt: time.Now().UTC(),
	}
	return c, v, nil
}

func (c *Config) NewVersion(content []byte, comment string, createdBy uuid.UUID) (*ConfigVersion, error) {
	if err := validateContent(content); err != nil {
		return nil, err
	}
	c.CurrentVersion++
	c.UpdatedAt = time.Now().UTC()
	return &ConfigVersion{
		ID:        uuid.New(),
		ConfigID:  c.ID,
		Version:   c.CurrentVersion,
		Content:   content,
		Comment:   comment,
		CreatedBy: createdBy,
		CreatedAt: time.Now().UTC(),
	}, nil
}

func validateContent(content []byte) error {
	if len(content) > maxContentSize {
		return ErrContentTooLarge
	}
	if !utf8.Valid(content) {
		return ErrInvalidUTF8
	}
	return nil
}
