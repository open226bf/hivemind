package notify_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/open226bf/hivemind/internal/adapters/notify"
	"github.com/open226bf/hivemind/internal/domain/monitoring"
)

func TestWebhookAlertRouter_Posts(t *testing.T) {
	bodyCh := make(chan []byte, 1)
	var contentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		contentType = r.Header.Get("Content-Type")
		b, _ := io.ReadAll(r.Body)
		bodyCh <- b
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	err := notify.NewWebhookAlertRouter(srv.URL).Route(context.Background(), monitoring.Alert{
		ID:       uuid.New(),
		State:    monitoring.AlertFiring,
		Severity: monitoring.SeverityWarning,
		Summary:  "redis CPU 95%",
		Labels:   map[string]string{"kind": "cpu_over"},
		FiredAt:  time.Now(),
	})
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, json.Unmarshal(<-bodyCh, &got))
	assert.Equal(t, "application/json", contentType)
	assert.Equal(t, "firing", got["state"])
	assert.Equal(t, "warning", got["severity"])
	assert.Equal(t, "cpu_over", got["kind"])
	assert.Equal(t, "redis CPU 95%", got["summary"])
}

func TestWebhookAlertRouter_NonZeroStatusIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	err := notify.NewWebhookAlertRouter(srv.URL).Route(context.Background(), monitoring.Alert{
		ID:     uuid.New(),
		Labels: map[string]string{},
	})
	assert.Error(t, err)
}
