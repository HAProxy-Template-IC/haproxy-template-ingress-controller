package proposalvalidator

import (
	"context"
	"errors"
	"fmt"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/rendercontext"
	"gitlab.com/haproxy-haptic/haptic/pkg/stores"
)

func (c *Service) validateAdmission(ctx context.Context, provider *stores.OverlayStoreProvider, opts ...rendercontext.Option) validationOutcome {
	outcome := c.runWithBaselineCheck(ctx, provider, opts...)
	if outcome.Admit || outcome.Phase != renderPhase || c.freshStoreProvider == nil ||
		ctx.Err() != nil || errors.Is(outcome.Error, context.Canceled) || errors.Is(outcome.Error, context.DeadlineExceeded) {
		return outcome
	}
	select {
	case c.refreshSlot <- struct{}{}:
		defer func() { <-c.refreshSlot }()
	case <-ctx.Done():
		return validationOutcome{Phase: renderPhase, Error: ctx.Err()}
	}
	fresh, err := c.freshStoreProvider(ctx)
	if err != nil {
		return validationOutcome{Phase: renderPhase, Error: fmt.Errorf("refreshing admission inputs: %w", err)}
	}
	if fresh == nil {
		return validationOutcome{Phase: renderPhase, Error: fmt.Errorf("refreshing admission inputs returned no stores")}
	}
	if err := ctx.Err(); err != nil {
		return validationOutcome{Phase: renderPhase, Error: err}
	}
	overlays := make(map[string]*stores.StoreOverlay)
	for _, name := range provider.StoreNames() {
		if overlay := provider.GetK8sOverlay(name); overlay != nil {
			overlays[name] = overlay
		}
	}
	refreshed := stores.NewOverlayStoreProvider(fresh, stores.NewValidationContext(overlays))
	if err := refreshed.Validate(); err != nil {
		return validationOutcome{Phase: "setup", Error: err}
	}
	validation := *c
	validation.baseStore = fresh
	return validation.runWithBaselineCheck(ctx, refreshed, opts...)
}
