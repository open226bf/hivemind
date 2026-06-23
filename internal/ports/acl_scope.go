package ports

import "context"

// aclListScopeKey is the private context key under which the per-request ACL
// list scope travels from the HTTP layer down to the repositories.
type aclListScopeKey struct{}

// ACLListScope bounds list queries to the resources a non-admin user may see.
// Clusters/Hives hold the ids (as strings, ready for SQL IN clauses) the user
// has any grant on. A non-nil scope with both lists empty means "deny all"
// (deny-by-default). A nil scope (admin, or ACL not enforced) means "no filter".
type ACLListScope struct {
	Clusters []string
	Hives    []string
}

// WithACLListScope attaches an ACL list scope to the context. Repositories read
// it via scopeACL to filter every list to the caller's authorized resources.
func WithACLListScope(ctx context.Context, s *ACLListScope) context.Context {
	return context.WithValue(ctx, aclListScopeKey{}, s)
}

// ACLListScopeFrom returns the ACL list scope carried by ctx, or nil when none
// was set (admin / shadow mode → no filtering).
func ACLListScopeFrom(ctx context.Context) *ACLListScope {
	s, _ := ctx.Value(aclListScopeKey{}).(*ACLListScope)
	return s
}
