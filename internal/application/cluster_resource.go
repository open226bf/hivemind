package application

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"github.com/open226bf/hivemind/internal/ports"
)

// ErrClusterRejected is returned when the target cluster is reachable but
// refuses to create the object (an invalid subnet, a name held by something
// else, a daemon error). The database write is rolled back, so nothing is left
// half-created. An unreachable cluster surfaces as ErrOrchestratorUnavailable
// instead — the caller can retry that one.
var ErrClusterRejected = errors.New("cluster refused the operation")

// resolveCluster returns the orchestrator driving a resource's cluster, or an
// error wrapping ErrOrchestratorUnavailable that names what could not be
// reached. Both halves of the create/delete pair go through it so an
// unreachable cluster reads the same either way.
func resolveCluster(ctx context.Context, registry ports.OrchestratorRegistry, clusterID uuid.UUID, what string) (ports.Orchestrator, error) {
	orch, err := registry.For(ctx, clusterID)
	if err == nil && orch == nil {
		err = errors.New("no orchestrator resolved")
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrOrchestratorUnavailable, what, err)
	}
	return orch, nil
}

// clusterResource describes a resource that exists on both sides — a row in the
// database and an object on the orchestrator — and the four steps needed to
// create it as one unit.
type clusterResource struct {
	// Persist writes the resource (and its first version) to the database.
	Persist func(ctx context.Context) error
	// Create materialises it on the cluster and returns the orchestrator id.
	Create func(ctx context.Context, orch ports.Orchestrator) (string, error)
	// Record stores that id next to the persisted rows.
	Record func(ctx context.Context, swarmID string) error
	// Remove deletes the cluster object again. It runs only when the commit
	// itself failed — the database then has no record of the resource, so the
	// object would otherwise be orphaned.
	Remove func(ctx context.Context, orch ports.Orchestrator, swarmID string) error
}

// create runs the four steps as a single unit: the row is written, the cluster
// object is created, its id is recorded, and only then does the transaction
// commit.
//
// The ordering is deliberate. Swarm has no transaction to enlist in, so exactly
// one side can roll back and it has to be the database. Persisting first means
// the cheap, likely failures — a duplicate name, a validation error — are
// rejected before anything is created on the cluster; creating before the
// commit means an unreachable cluster rolls the row back instead of leaving a
// resource Hivemind believes in and the cluster has never heard of.
//
// The transaction does stay open across a call to the Docker API. It holds only
// the rows just inserted, which nothing else is reading yet, so the cost is a
// connection held for the duration of one API call.
//
// registry may be nil — deployments wired without an orchestrator (and the
// route smoke tests) then keep the previous database-only behaviour.
func (r clusterResource) create(
	ctx context.Context,
	tx ports.TxManager,
	registry ports.OrchestratorRegistry,
	clusterID uuid.UUID,
	what string,
) error {
	if tx == nil || registry == nil {
		return r.Persist(ctx)
	}

	orch, err := resolveCluster(ctx, registry, clusterID, what)
	if err != nil {
		return err
	}

	var swarmID string
	txErr := tx.WithinTx(ctx, func(ctx context.Context) error {
		if err := r.Persist(ctx); err != nil {
			return err
		}
		id, err := r.Create(ctx, orch)
		if err != nil {
			return fmt.Errorf("%w: create %s on cluster: %w", ErrClusterRejected, what, err)
		}
		swarmID = id
		return r.Record(ctx, id)
	})
	if txErr != nil && swarmID != "" {
		// The cluster object outlived the transaction that was meant to own it.
		// Take it back down so a retry starts from a clean slate; use the outer
		// context, the transactional one is done.
		if err := r.Remove(ctx, orch, swarmID); err != nil {
			slog.Error("orphaned cluster object after rollback",
				"resource", what, "swarm_id", swarmID, "err", err)
		}
	}
	return txErr
}

// clusterDeletion describes removing a resource from both sides at once.
type clusterDeletion struct {
	// SwarmIDs collects the orchestrator ids the resource owns. It runs before
	// the rows are deleted, since that is where the ids live.
	SwarmIDs func(ctx context.Context) ([]string, error)
	// Delete removes the database rows.
	Delete func(ctx context.Context) error
	// Remove deletes one cluster object.
	Remove func(ctx context.Context, orch ports.Orchestrator, swarmID string) error
}

// run deletes the rows and the cluster objects they own as one unit, mirroring
// clusterResource.create.
//
// Removing the cluster object matters beyond tidiness: Swarm secrets and
// configs are addressed by name, and creating one whose name is already taken
// silently returns the existing object. Leaving <name>_v1 behind would mean a
// config deleted and re-created under the same name serves its *old* content,
// with the database showing the new one.
//
// The rows go first so the interesting failure is the safe one: a cluster that
// refuses the removal (an unmanaged service still mounts the object) rolls the
// delete back and both sides stay as they were. A commit that fails after the
// object is gone leaves a row the next deployment recreates from its stored
// content, so that case heals itself.
func (d clusterDeletion) run(
	ctx context.Context,
	tx ports.TxManager,
	registry ports.OrchestratorRegistry,
	clusterID uuid.UUID,
	what string,
) error {
	if tx == nil || registry == nil {
		return d.Delete(ctx)
	}

	orch, err := resolveCluster(ctx, registry, clusterID, what)
	if err != nil {
		return err
	}

	return tx.WithinTx(ctx, func(ctx context.Context) error {
		ids, err := d.SwarmIDs(ctx)
		if err != nil {
			return err
		}
		if err := d.Delete(ctx); err != nil {
			return err
		}
		for _, id := range ids {
			if id == "" {
				continue // never materialised (created before this became atomic)
			}
			if err := d.Remove(ctx, orch, id); err != nil {
				return fmt.Errorf("%w: remove %s from cluster: %w", ErrClusterRejected, what, err)
			}
		}
		return nil
	})
}
