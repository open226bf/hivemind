// Package hive models a project that groups services (F — "ruches"). A service
// belongs to at most one hive; the hive is purely organisational.
package hive

import (
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

var (
	ErrInvalidName  = errors.New("hive name must be 1–64 characters")
	ErrInvalidColor = errors.New("hive color must be a hex code like #1e88e5")
	ErrHiveNotEmpty = errors.New("hive still contains services")
)

const maxNameLen = 64

var colorRegex = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// Hive is a named project grouping services. Color tints the hexagon tiles in
// the UI; empty means "use the default".
type Hive struct {
	ID          uuid.UUID
	Name        string
	Description string
	Color       string // optional "#RRGGBB"
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

func New(name, description, color string) (*Hive, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > maxNameLen {
		return nil, ErrInvalidName
	}
	if err := validateColor(color); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	return &Hive{
		ID:          uuid.New(),
		Name:        name,
		Description: description,
		Color:       color,
		CreatedAt:   now,
		UpdatedAt:   now,
	}, nil
}

// Update mutates the editable fields (name, description, color).
func (h *Hive) Update(name, description, color string) error {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > maxNameLen {
		return ErrInvalidName
	}
	if err := validateColor(color); err != nil {
		return err
	}
	h.Name = name
	h.Description = description
	h.Color = color
	h.UpdatedAt = time.Now().UTC()
	return nil
}

func validateColor(color string) error {
	if color == "" {
		return nil
	}
	if !colorRegex.MatchString(color) {
		return ErrInvalidColor
	}
	return nil
}
