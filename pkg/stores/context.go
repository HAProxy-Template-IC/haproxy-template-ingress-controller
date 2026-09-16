package stores

import "context"

// ContextGetter is the optional cancellation-aware Get extension to Store.
type ContextGetter interface {
	GetContext(ctx context.Context, keys ...string) ([]any, error)
}

// ContextLister is the optional cancellation-aware List extension to Store.
type ContextLister interface {
	ListContext(ctx context.Context) ([]any, error)
}

// GetContext uses the store's context-aware read when available. Legacy reads check
// cancellation before and after the call but cannot interrupt a read in progress.
func GetContext(ctx context.Context, store Store, keys ...string) ([]any, error) {
	if contextual, ok := store.(ContextGetter); ok {
		return contextual.GetContext(ctx, keys...)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	items, err := store.Get(keys...)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	return items, err
}

// ListContext uses the store's context-aware read or checks cancellation around List.
func ListContext(ctx context.Context, store Store) ([]any, error) {
	if contextual, ok := store.(ContextLister); ok {
		return contextual.ListContext(ctx)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	items, err := store.List()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	return items, err
}
