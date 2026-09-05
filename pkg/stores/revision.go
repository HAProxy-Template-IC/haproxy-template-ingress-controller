package stores

import (
	"context"
	"errors"
)

// Revision is a comparable token for one store read scope. An empty token is unsupported.
type Revision string

// RevisionSource identifies one store instance for its lifetime. Zero is unsupported.
type RevisionSource uint64

// Revisioned exposes read-scope revisions without loading resource bodies.
type Revisioned interface {
	ListRevision() Revision
	GetRevision(keys ...string) Revision
	IdentityRevision(namespace, name string) Revision
}

// RevisionChange describes one resource-agnostic store mutation.
type RevisionChange struct {
	Sequence  uint64
	Namespace string
	Name      string
	Deleted   bool
	OldKeys   []string
	NewKeys   []string
}

// RevisionJournal exposes an atomic baseline and bounded changes after it.
type RevisionJournal interface {
	ListSnapshot() (items []any, sequence uint64, err error)
	ChangesSince(sequence uint64) (current uint64, changes []RevisionChange, complete bool)
}

// ExactRevisionJournal guarantees that every retained change carries the exact
// identity and complete old/new index keys for the named revision source.
type ExactRevisionJournal interface {
	RevisionJournal
	ExactRevisionJournalSource() RevisionSource
}

// IdentityGetter retrieves a resource without knowing its configured index keys.
type IdentityGetter interface {
	GetIdentity(namespace, name string) (resource any, found bool, err error)
}

// SnapshotReader binds exact read results to their revision and journal watermark.
type SnapshotReader interface {
	RevisionSource() RevisionSource
	GetSnapshot(keys ...string) (items []any, revision Revision, sequence uint64, err error)
	IdentitySnapshot(namespace, name string) (
		item any, found bool, revision Revision, sequence uint64, err error,
	)
}

// ReadSnapshot is one immutable store root. Reads return detached values, and
// every revision comes from the snapshot's fixed source and sequence.
type ReadSnapshot interface {
	Revisioned
	IdentityGetter
	RevisionSource() RevisionSource
	Sequence() uint64
	Get(keys ...string) ([]any, error)
	List() ([]any, error)
}

// IdentityOrderedReadSnapshot guarantees List and Get order solely by namespace/name.
type IdentityOrderedReadSnapshot interface {
	ReadSnapshot
	IdentityOrderSource() RevisionSource
}

// ContextReadSnapshot cancels reads that can perform external I/O.
type ContextReadSnapshot interface {
	ReadSnapshot
	GetContext(ctx context.Context, keys ...string) ([]any, error)
	ListContext(ctx context.Context) ([]any, error)
	GetIdentityContext(ctx context.Context, namespace, name string) (item any, found bool, err error)
}

// SnapshotProvider pins an immutable store root without loading its resources.
type SnapshotProvider interface {
	Pin() (ReadSnapshot, error)
}

// ErrIdentityLookupUnsupported means an adapter cannot perform an exact identity read.
var ErrIdentityLookupUnsupported = errors.New("exact identity lookup is unsupported")

// ErrSnapshotUnsupported means a store cannot bind a read to a revision watermark.
var ErrSnapshotUnsupported = errors.New("atomic store snapshot is unsupported")

// ErrSnapshotChanged means a lazy snapshot read could not reproduce its pinned root.
var ErrSnapshotChanged = errors.New("store changed after its snapshot was pinned")
