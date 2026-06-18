package dto

// PortDTO is one published-port mapping of a service.
type PortDTO struct {
	TargetPort    uint32 `json:"target_port"`    // container port (1–65535)
	PublishedPort uint32 `json:"published_port"` // host/ingress port (0 = auto)
	Protocol      string `json:"protocol"`       // tcp | udp | sctp (default tcp)
	Mode          string `json:"mode"`           // ingress | host (default ingress)
}

// SetPortsRequest is the body for PUT /services/{id}/ports (full replacement).
type SetPortsRequest struct {
	Ports []PortDTO `json:"ports"`
}

// PortsResponse wraps the published ports of a service.
type PortsResponse struct {
	Ports []PortDTO `json:"ports"`
}
