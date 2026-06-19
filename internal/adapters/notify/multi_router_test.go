package notify_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/orange/hivemind/internal/adapters/notify"
	"github.com/orange/hivemind/internal/domain/monitoring"
)

type capRouter struct {
	called int
	err    error
}

func (c *capRouter) Route(context.Context, monitoring.Alert) error {
	c.called++
	return c.err
}

func TestMultiRouter_FansOutAndJoinsErrors(t *testing.T) {
	a := &capRouter{}
	b := &capRouter{err: errors.New("boom")}
	c := &capRouter{}

	// nil routers are skipped; one failure doesn't stop the others.
	err := notify.NewMultiRouter(a, b, nil, c).Route(context.Background(), monitoring.Alert{})

	assert.ErrorContains(t, err, "boom")
	assert.Equal(t, 1, a.called)
	assert.Equal(t, 1, b.called)
	assert.Equal(t, 1, c.called)
}

func TestMultiRouter_AllOK(t *testing.T) {
	a, b := &capRouter{}, &capRouter{}
	assert.NoError(t, notify.NewMultiRouter(a, b).Route(context.Background(), monitoring.Alert{}))
}
