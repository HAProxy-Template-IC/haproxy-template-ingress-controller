package tests

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/types"
)

// TestDefaultsConsistency_DebounceShared pins the equality of the per-watcher
// debounce default and the reconciler-refractory default.
//
// pkg/core/config and pkg/k8s/types live in different architectural layers
// and cannot import each other (arch-go.yml forbids it), so the constants are
// duplicated by design. This test is the single guardrail: if the watcher
// default ever changes, this check fails and forces the reconciler default to
// move with it (or vice versa). Document any intentional split here.
func TestDefaultsConsistency_DebounceShared(t *testing.T) {
	assert.Equal(t,
		types.DefaultDebounceInterval,
		config.DefaultReconciliationDebounceInterval,
		"per-watcher debounce default and reconciler refractory default must stay equal; update both at the same time")
}
