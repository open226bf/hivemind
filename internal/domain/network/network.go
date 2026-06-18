package network

import (
	"errors"
	"regexp"
	"time"

	"github.com/google/uuid"
)

var (
	ErrNetworkInUse = errors.New("network is attached to one or more services")
	ErrInvalidName  = errors.New("network name must match ^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,62}$")
)

var nameRegex = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,62}$`)

type Network struct {
	ID         uuid.UUID
	ClusterID  uuid.UUID // orchestration target; zero value = the default cluster
	Name       string
	Driver     string // overlay
	Scope      string
	Subnet     string // IPAM subnet, e.g. "10.0.9.0/24"; empty = Docker default
	Attachable bool
	External   bool   // created outside the platform
	SwarmID    string // Swarm network ID
	CreatedAt  time.Time
}

func New(name string) (*Network, error) {
	if !nameRegex.MatchString(name) {
		return nil, ErrInvalidName
	}
	return &Network{
		ID:         uuid.New(),
		Name:       name,
		Driver:     "overlay",
		Scope:      "swarm",
		Attachable: true,
		External:   false,
		CreatedAt:  time.Now().UTC(),
	}, nil
}
