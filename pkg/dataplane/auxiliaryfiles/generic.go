package auxiliaryfiles

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"golang.org/x/sync/errgroup"
)

// maxAuxSyncConcurrency bounds how many auxiliary-file storage round-trips run
// concurrently within a single file type. Each Create/Update/Delete is an
// independent skip_reload storage write to a distinct path — no transaction, no
// inter-file dependency — so they parallelize safely. Bounding the fan-out
// keeps a large churned changeset (hundreds of map/general files across all
// tenants) from opening an unbounded number of Dataplane API connections, while
// collapsing the per-file round-trip latency that would otherwise serialize
// into a multi-second pre-config phase. Under CI's slow per-op latency that
// serial phase is the window during which the deploy loop's runtime fast path
// is dark and a rolling-restart endpoint change cannot propagate — the residual
// zero-downtime 503.
const maxAuxSyncConcurrency = 16

// noFingerprintSentinel is the placeholder content older API versions
// (or stale storage metadata) return when they have an entry but no
// content fingerprint. categorizeFile must always route through UPDATE
// when it sees this value because we can't trust the API's reported
// content. Kept as a package-level constant (not a literal) so the test
// suite can reference the same value — a future rename surfaces as a
// compile error on both sides instead of silent behavioural drift.
const noFingerprintSentinel = "__NO_FINGERPRINT__"

// isAlreadyExistsError checks whether err indicates that a resource already exists.
// This is used across file operations to fall back to update when a create fails
// because the file physically exists but wasn't in the storage listing.
func isAlreadyExistsError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "already exists")
}

// clientFileOps implements FileOperations[T] by delegating to function values
// pulled from the DataplaneClient. The Create wrapper falls back to Update on
// "already exists" errors, which is the standard recovery for storage that has
// the file on disk but missing from the storage listing (e.g. after a raw
// config push + reload). The optional idForAPI hook normalizes the controller
// side identifier before each per-id API call (e.g. path.Base for storage
// APIs that take a filename rather than a full path).
type clientFileOps[T FileItem] struct {
	getAll     func(context.Context) ([]string, error)
	getContent func(context.Context, string) (string, error)
	create     func(context.Context, string, string) (string, error)
	update     func(context.Context, string, string) (string, error)
	deleteFn   func(context.Context, string) error
	idForAPI   func(string) string
}

func (o *clientFileOps[T]) apiID(id string) string {
	if o.idForAPI == nil {
		return id
	}
	return o.idForAPI(id)
}

func (o *clientFileOps[T]) GetAll(ctx context.Context) ([]string, error) {
	return o.getAll(ctx)
}

func (o *clientFileOps[T]) GetContent(ctx context.Context, id string) (string, error) {
	return o.getContent(ctx, o.apiID(id))
}

func (o *clientFileOps[T]) Create(ctx context.Context, id, content string) (string, error) {
	reloadID, err := o.create(ctx, o.apiID(id), content)
	if isAlreadyExistsError(err) {
		return o.Update(ctx, id, content)
	}
	return reloadID, err
}

func (o *clientFileOps[T]) Update(ctx context.Context, id, content string) (string, error) {
	return o.update(ctx, o.apiID(id), content)
}

func (o *clientFileOps[T]) Delete(ctx context.Context, id string) error {
	return o.deleteFn(ctx, o.apiID(id))
}

// FileItem represents any auxiliary file type (GeneralFile, SSLCertificate, MapFile).
//
// All auxiliary file types must implement this interface to work with the
// generic Compare and Sync functions.
type FileItem interface {
	// GetIdentifier returns the unique identifier for this file (filename or path).
	GetIdentifier() string

	// GetContent returns the file content.
	GetContent() string
}

// FileOperations defines CRUD operations for a specific auxiliary file type.
//
// Implementations wrap the DataplaneClient methods for general files, SSL certificates,
// or map files.
type FileOperations[T FileItem] interface {
	// GetAll returns all file identifiers (filenames/paths) currently stored.
	GetAll(ctx context.Context) ([]string, error)

	// GetContent retrieves the content for a specific file by identifier.
	GetContent(ctx context.Context, id string) (string, error)

	// Create creates a new file with the given identifier and content.
	// Returns the reload ID if a reload was triggered (empty string if not).
	Create(ctx context.Context, id, content string) (string, error)

	// Update updates an existing file with new content.
	// Returns the reload ID if a reload was triggered (empty string if not).
	Update(ctx context.Context, id, content string) (string, error)

	// Delete removes a file by identifier.
	Delete(ctx context.Context, id string) error
}

// FileDiffGeneric represents the differences between current and desired file states.
//
// This is a generic version of FileDiff/SSLCertificateDiff/MapFileDiff that works
// with any FileItem type.
type FileDiffGeneric[T FileItem] struct {
	// ToCreate contains files that exist in the desired state but not in the current state.
	ToCreate []T

	// ToUpdate contains files that exist in both states but have different content.
	ToUpdate []T

	// ToDelete contains identifiers of files that exist in the current state but not in the desired state.
	ToDelete []string
}

// HasChanges returns true if the diff contains any create, update, or delete operations.
func (d *FileDiffGeneric[T]) HasChanges() bool {
	return len(d.ToCreate) > 0 || len(d.ToUpdate) > 0 || len(d.ToDelete) > 0
}

// categorizeFile determines whether a file should be created, updated, or left unchanged.
func categorizeFile[T FileItem](currentMap map[string]T, id string, desiredFile T, diff *FileDiffGeneric[T]) {
	currentFile, exists := currentMap[id]
	if !exists {
		// File doesn't exist in current state → create
		diff.ToCreate = append(diff.ToCreate, desiredFile)
		return
	}

	// File exists - check if content differs
	currentContent := currentFile.GetContent()
	desiredContent := desiredFile.GetContent()

	// Special case: If current content is the no-fingerprint sentinel, use UPDATE.
	// This happens when the API has metadata but no content fingerprint (older API versions
	// or stale metadata). UPDATE works whether the file physically exists or not, and is
	// idempotent. Using CREATE would fail with 409 Conflict if metadata exists.
	if currentContent == noFingerprintSentinel {
		diff.ToUpdate = append(diff.ToUpdate, desiredFile)
	} else if currentContent != desiredContent {
		// File exists and content differs → update
		diff.ToUpdate = append(diff.ToUpdate, desiredFile)
	}
	// If content is identical, no action needed
}

// Compare compares the current state of files with the desired state using generic operations.
//
// This function:
//  1. Fetches all current file identifiers from the API
//  2. Downloads content for each current file
//  3. Compares with the desired files list
//  4. Returns a FileDiffGeneric with operations needed to reach desired state
//
// Type Parameters:
//   - T: The file item type (must implement FileItem interface)
//
// Parameters:
//   - ctx: Context for cancellation
//   - ops: File operations adapter for the specific file type
//   - desired: Desired file state
//   - newFile: Constructor function to create a new file item from identifier and content
//
// Returns:
//   - *FileDiffGeneric[T]: Diff containing create, update, and delete operations
//   - error: Any error encountered during comparison
func Compare[T FileItem](
	ctx context.Context,
	ops FileOperations[T],
	desired []T,
	newFile func(id, content string) T,
) (*FileDiffGeneric[T], error) {
	// Fetch current file identifiers from API
	currentIDs, err := ops.GetAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("fetching current files: %w", err)
	}

	// Download content for all current files in parallel
	currentFiles := make([]T, len(currentIDs))

	g, gCtx := errgroup.WithContext(ctx)

	for i, id := range currentIDs {
		g.Go(func() error {
			content, err := ops.GetContent(gCtx, id)
			if err != nil {
				return fmt.Errorf("getting content for file '%s': %w", id, err)
			}

			// Safe to write directly - each goroutine has unique index
			currentFiles[i] = newFile(id, content)

			return nil
		})
	}

	// Wait for all file content fetches to complete
	if err := g.Wait(); err != nil {
		return nil, err
	}

	// Build maps for easier comparison
	currentMap := make(map[string]T)
	for _, file := range currentFiles {
		currentMap[file.GetIdentifier()] = file
	}

	desiredMap := make(map[string]T)
	for _, file := range desired {
		desiredMap[file.GetIdentifier()] = file
	}

	diff := &FileDiffGeneric[T]{
		ToCreate: []T{},
		ToUpdate: []T{},
		ToDelete: []string{},
	}

	// Find files to create or update
	for id, desiredFile := range desiredMap {
		categorizeFile(currentMap, id, desiredFile, diff)
	}

	// Find files to delete (exist in current but not in desired)
	for id := range currentMap {
		if _, exists := desiredMap[id]; !exists {
			diff.ToDelete = append(diff.ToDelete, id)
		}
	}

	return diff, nil
}

// Sync synchronizes files to the desired state by applying the provided diff.
//
// This function should be called in two phases:
//   - Phase 1 (pre-config): Call with diff containing ToCreate and ToUpdate
//   - Phase 2 (post-config): Call with diff containing ToDelete
//
// The caller is responsible for splitting the diff into these phases.
//
// Type Parameters:
//   - T: The file item type (must implement FileItem interface)
//
// Parameters:
//   - ctx: Context for cancellation
//   - ops: File operations adapter for the specific file type
//   - diff: The diff to apply (may contain create, update, and/or delete operations)
//
// Returns:
//   - []string: Reload IDs from create/update operations that triggered reloads
//   - error: Any error encountered during synchronization
func Sync[T FileItem](
	ctx context.Context,
	ops FileOperations[T],
	diff *FileDiffGeneric[T],
) ([]string, error) {
	if diff == nil {
		return nil, nil
	}

	var (
		mu        sync.Mutex
		reloadIDs []string
	)
	addReload := func(id string) {
		if id == "" {
			return
		}
		mu.Lock()
		reloadIDs = append(reloadIDs, id)
		mu.Unlock()
	}

	// Phases run in order (create → update → delete) — deletes must not race a
	// create/update of the same path, and a reload triggered mid-phase must see
	// all of that phase's files. Within a phase every operation is over a
	// distinct path with no inter-file dependency, so they run concurrently
	// (bounded) to collapse per-file round-trip latency. The reload-ID order is
	// no longer the input order (completion order), which callers don't rely on.
	if err := applyAuxConcurrently(ctx, diff.ToCreate, func(c context.Context, file T) error {
		reloadID, err := ops.Create(c, file.GetIdentifier(), file.GetContent())
		if err != nil {
			return fmt.Errorf("creating file '%s': %w", file.GetIdentifier(), err)
		}
		addReload(reloadID)
		return nil
	}); err != nil {
		return nil, err
	}
	if err := applyAuxConcurrently(ctx, diff.ToUpdate, func(c context.Context, file T) error {
		reloadID, err := ops.Update(c, file.GetIdentifier(), file.GetContent())
		if err != nil {
			return fmt.Errorf("updating file '%s': %w", file.GetIdentifier(), err)
		}
		addReload(reloadID)
		return nil
	}); err != nil {
		return nil, err
	}
	if err := applyAuxConcurrently(ctx, diff.ToDelete, func(c context.Context, id string) error {
		if err := ops.Delete(c, id); err != nil {
			return fmt.Errorf("deleting file '%s': %w", id, err)
		}
		return nil
	}); err != nil {
		return nil, err
	}

	return reloadIDs, nil
}

// applyAuxConcurrently runs fn over each item with bounded concurrency
// (maxAuxSyncConcurrency), failing fast: the first error cancels the shared
// context and is returned once the in-flight operations unwind.
func applyAuxConcurrently[E any](ctx context.Context, items []E, fn func(context.Context, E) error) error {
	if len(items) == 0 {
		return nil
	}
	g, gCtx := errgroup.WithContext(ctx)
	g.SetLimit(maxAuxSyncConcurrency)
	for _, item := range items {
		g.Go(func() error { return fn(gCtx, item) })
	}
	return g.Wait()
}
