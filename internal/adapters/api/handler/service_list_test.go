package handler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/open226bf/hivemind/internal/adapters/api/handler"
	"github.com/open226bf/hivemind/internal/application"
	"github.com/open226bf/hivemind/internal/domain/service"
	"github.com/open226bf/hivemind/internal/ports"
	"github.com/open226bf/hivemind/pkg/pagination"
)

// listRepo serves a fixed page. Embedding the interface leaves every method the
// list endpoint never calls nil, so a stray call would fail loudly.
type listRepo struct {
	ports.ServiceRepository
	items []*service.Service
	ports map[uuid.UUID][]service.Port
}

func (r listRepo) List(context.Context, ports.ServiceFilter, pagination.Page) ([]*service.Service, int64, error) {
	return r.items, int64(len(r.items)), nil
}

func (r listRepo) GetPortsByServices(_ context.Context, ids []uuid.UUID) (map[uuid.UUID][]service.Port, error) {
	out := map[uuid.UUID][]service.Port{}
	for _, id := range ids {
		if p := r.ports[id]; len(p) > 0 {
			out[id] = p
		}
	}
	return out, nil
}

func getList(t *testing.T, repo listRepo) map[string]any {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := handler.NewServiceHandler(application.NewServiceService(repo, nil))
	r.GET("/services", h.List)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/services", nil))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	return body
}

func TestServiceList_CarriesPublishedPortsPerRow(t *testing.T) {
	web, err := service.New("web", "nginx", "latest", 1, nil)
	require.NoError(t, err)
	worker, err := service.New("worker", "busybox", "latest", 1, nil)
	require.NoError(t, err)

	body := getList(t, listRepo{
		items: []*service.Service{web, worker},
		ports: map[uuid.UUID][]service.Port{
			web.ID: {
				{TargetPort: 80, PublishedPort: 8080, Protocol: "tcp", Mode: "ingress"},
				{TargetPort: 53, PublishedPort: 5353, Protocol: "udp", Mode: "host"},
			},
		},
	})

	items := body["items"].([]any)
	require.Len(t, items, 2)

	first := items[0].(map[string]any)
	// The embedded ServiceResponse must stay flat: clients read name, not
	// a nested object.
	assert.Equal(t, "web", first["name"])
	assert.Equal(t, "nginx:latest", first["full_image"])
	ports := first["ports"].([]any)
	require.Len(t, ports, 2)
	assert.Equal(t, map[string]any{
		"target_port": float64(80), "published_port": float64(8080),
		"protocol": "tcp", "mode": "ingress",
	}, ports[0])
	assert.Equal(t, "udp", ports[1].(map[string]any)["protocol"])
}

func TestServiceList_NoPortsIsAnEmptyArrayNotNull(t *testing.T) {
	worker, err := service.New("worker", "busybox", "latest", 1, nil)
	require.NoError(t, err)

	body := getList(t, listRepo{items: []*service.Service{worker}})

	row := body["items"].([]any)[0].(map[string]any)
	ports, present := row["ports"]
	require.True(t, present, "the field must always be sent")
	assert.NotNil(t, ports, "null is not an empty list: clients iterate it")
	assert.Empty(t, ports)
}
