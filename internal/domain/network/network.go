package network

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

var ErrNetworkInUse = errors.New("network is attached to one or more services")

type Network struct {
	ID         uuid.UUID
	Name       string
	Driver     string // overlay
	Scope      string
	Attachable bool
	External   bool   // created outside the platform
	SwarmID    string // Swarm network ID
	CreatedAt  time.Time
}

func New(name string) *Network {
	return &Network{
		ID:         uuid.New(),
		Name:       name,
		Driver:     "overlay",
		Scope:      "swarm",
		Attachable: true,
		External:   false,
		CreatedAt:  time.Now().UTC(),
	}
}
