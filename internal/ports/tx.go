package ports

import "context"

// TxManager runs a unit of work inside a single database transaction.
//
// It exists so a use case can make several repository writes — and the
// orchestrator call that must succeed with them — atomic, without the
// application layer knowing anything about the storage technology. The context
// handed to fn carries the open transaction: repository calls made with it join
// it, calls made with any other context do not.
type TxManager interface {
	// WithinTx calls fn inside a transaction, committing when it returns nil and
	// rolling back on any error. Nesting is transparent: calling WithinTx again
	// with the transactional context reuses the running transaction.
	WithinTx(ctx context.Context, fn func(ctx context.Context) error) error
}
