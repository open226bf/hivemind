package persistence

import (
	"context"

	"gorm.io/gorm"
)

// txKey carries the open transaction on the context. Unexported so only this
// package can put one there — a repository joins a transaction by asking
// dbFor, never by unwrapping the context itself.
type txKey struct{}

// TxManager implements ports.TxManager on GORM.
type TxManager struct{ db *gorm.DB }

func NewTxManager(db *gorm.DB) *TxManager { return &TxManager{db: db} }

// WithinTx runs fn inside a transaction and commits when it returns nil; any
// error (or panic) rolls the whole thing back. The context passed to fn carries
// the transaction, so every repository call made with it is part of it.
//
// Already inside a transaction, fn simply runs on the running one: the caller
// gets the same all-or-nothing guarantee without a nested savepoint.
func (m *TxManager) WithinTx(ctx context.Context, fn func(ctx context.Context) error) error {
	if txFrom(ctx) != nil {
		return fn(ctx)
	}
	return m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(context.WithValue(ctx, txKey{}, tx))
	})
}

func txFrom(ctx context.Context) *gorm.DB {
	tx, _ := ctx.Value(txKey{}).(*gorm.DB)
	return tx
}

// dbFor returns the handle a repository must run its statement on: the ambient
// transaction when the caller opened one, the repository's own pool otherwise.
// Repositories call it instead of db.WithContext so they take part in a
// transaction the use case owns without being aware of it.
func dbFor(ctx context.Context, db *gorm.DB) *gorm.DB {
	if tx := txFrom(ctx); tx != nil {
		// Re-attach the context: list queries read the per-request ACL scope
		// from the statement context (see scopeACL).
		return tx.WithContext(ctx)
	}
	return db.WithContext(ctx)
}
