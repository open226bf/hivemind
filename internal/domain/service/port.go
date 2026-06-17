package service

import "errors"

var (
	ErrInvalidPortTarget    = errors.New("target port must be between 1 and 65535")
	ErrInvalidPortPublished = errors.New("published port must be between 0 and 65535")
	ErrInvalidPortProtocol  = errors.New("port protocol must be one of: tcp, udp, sctp")
	ErrInvalidPortMode      = errors.New("port publish mode must be one of: ingress, host")
	ErrDuplicatePort        = errors.New("a published port/protocol pair is declared twice")
)

// Published-port modes (Swarm).
const (
	PortModeIngress = "ingress" // routing-mesh load balancing across the cluster
	PortModeHost    = "host"    // publish only on nodes running a task
)

var validPortProtocols = map[string]bool{"tcp": true, "udp": true, "sctp": true}
var validPortModes = map[string]bool{PortModeIngress: true, PortModeHost: true}

// Port is one published-port mapping for a service: the container's TargetPort
// is exposed on PublishedPort. A PublishedPort of 0 lets Swarm auto-assign one.
type Port struct {
	TargetPort    uint32 // container port (1–65535)
	PublishedPort uint32 // host/ingress port (0 = auto-assigned by Swarm)
	Protocol      string // tcp | udp | sctp
	Mode          string // ingress | host
}

// Validate normalises defaults (empty protocol→tcp, empty mode→ingress) and
// checks the bounds and vocabulary.
func (p *Port) Validate() error {
	if p.Protocol == "" {
		p.Protocol = "tcp"
	}
	if p.Mode == "" {
		p.Mode = PortModeIngress
	}
	if p.TargetPort < 1 || p.TargetPort > 65535 {
		return ErrInvalidPortTarget
	}
	if p.PublishedPort > 65535 {
		return ErrInvalidPortPublished
	}
	if !validPortProtocols[p.Protocol] {
		return ErrInvalidPortProtocol
	}
	if !validPortModes[p.Mode] {
		return ErrInvalidPortMode
	}
	return nil
}

// ValidatePorts validates each port and rejects duplicate published-port/protocol
// pairs (a non-zero published port may be claimed only once). Entries are
// normalised in place.
func ValidatePorts(ports []Port) error {
	seen := make(map[string]bool, len(ports))
	for i := range ports {
		if err := ports[i].Validate(); err != nil {
			return err
		}
		if ports[i].PublishedPort == 0 {
			continue
		}
		key := ports[i].Protocol + "/" + portKey(ports[i].PublishedPort)
		if seen[key] {
			return ErrDuplicatePort
		}
		seen[key] = true
	}
	return nil
}

func portKey(p uint32) string {
	// small, allocation-free uint→string for map keys
	if p == 0 {
		return "0"
	}
	var buf [10]byte
	i := len(buf)
	for p > 0 {
		i--
		buf[i] = byte('0' + p%10)
		p /= 10
	}
	return string(buf[i:])
}
