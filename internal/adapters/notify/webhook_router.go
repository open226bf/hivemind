package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/orange/hivemind/internal/domain/monitoring"
	"github.com/orange/hivemind/internal/ports"
)

// WebhookAlertRouter POSTs each alert (fire and resolve) as JSON to a configured
// URL — a generic sink that works with incoming-webhook receivers (n8n, a custom
// relay, Slack/Discord via a small adapter). Per-hive/cluster channels are a
// later phase; this is the zero-config single-endpoint MVP.
type WebhookAlertRouter struct {
	url    string
	client *http.Client
}

func NewWebhookAlertRouter(url string) WebhookAlertRouter {
	return WebhookAlertRouter{url: url, client: &http.Client{Timeout: 10 * time.Second}}
}

// webhookPayload is the stable JSON shape posted to the webhook.
type webhookPayload struct {
	ID          string     `json:"id"`
	State       string     `json:"state"` // firing | resolved
	Severity    string     `json:"severity"`
	Kind        string     `json:"kind"`
	ClusterID   string     `json:"cluster_id,omitempty"`
	ServiceID   string     `json:"service_id,omitempty"`
	NodeID      string     `json:"node_id,omitempty"`
	ContainerID string     `json:"container_id,omitempty"`
	Summary     string     `json:"summary"`
	Detail      string     `json:"detail,omitempty"`
	FiredAt     time.Time  `json:"fired_at"`
	ResolvedAt  *time.Time `json:"resolved_at,omitempty"`
}

func (r WebhookAlertRouter) Route(ctx context.Context, a monitoring.Alert) error {
	svc := ""
	if a.ServiceID != nil {
		svc = a.ServiceID.String()
	}
	cluster := ""
	if a.ClusterID != uuid.Nil {
		cluster = a.ClusterID.String()
	}

	body, err := json.Marshal(webhookPayload{
		ID:          a.ID.String(),
		State:       string(a.State),
		Severity:    string(a.Severity),
		Kind:        a.Labels["kind"],
		ClusterID:   cluster,
		ServiceID:   svc,
		NodeID:      a.NodeID,
		ContainerID: a.ContainerID,
		Summary:     a.Summary,
		Detail:      a.Detail,
		FiredAt:     a.FiredAt,
		ResolvedAt:  a.ResolvedAt,
	})
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := r.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("alert webhook returned %s", resp.Status)
	}
	return nil
}

var _ ports.AlertRouter = WebhookAlertRouter{}
