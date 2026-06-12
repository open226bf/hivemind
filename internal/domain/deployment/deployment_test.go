package deployment_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/open226bf/hivemind/internal/domain/deployment"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newDep() *deployment.Deployment {
	svcID := uuid.New()
	return deployment.New(svcID, nil, "v1.0.0", deployment.TriggerWebhook, nil)
}

func TestNew_InitialState(t *testing.T) {
	d := newDep()
	assert.Equal(t, deployment.StatusPending, d.Status)
	assert.Nil(t, d.FinishedAt)
	assert.False(t, d.IsTerminal())
}

func TestDeployment_LifecycleSucceed(t *testing.T) {
	d := newDep()
	d.Start()
	assert.Equal(t, deployment.StatusInProgress, d.Status)

	d.Succeed()
	assert.Equal(t, deployment.StatusSucceeded, d.Status)
	assert.NotNil(t, d.FinishedAt)
	assert.True(t, d.IsTerminal())

	dur := d.Duration()
	require.NotNil(t, dur)
	assert.True(t, *dur >= 0)
}

func TestDeployment_LifecycleFail(t *testing.T) {
	d := newDep()
	d.Start()
	d.Fail("health check timed out")
	assert.Equal(t, deployment.StatusFailed, d.Status)
	assert.Equal(t, "health check timed out", d.ErrorMessage)
	assert.True(t, d.IsTerminal())
}

func TestDeployment_RolledBack(t *testing.T) {
	d := newDep()
	d.MarkRolledBack()
	assert.Equal(t, deployment.StatusRolledBack, d.Status)
	assert.True(t, d.IsTerminal())
}
