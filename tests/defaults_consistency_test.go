package tests

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"gitlab.com/haproxy-haptic/haptic/pkg/core/config"
	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/types"
)

// TestDefaultsConsistency_DebounceImmediateShared pins the equality of the
// DebounceImmediate sentinel across the same two packages, for the same
// reason: pkg/core/config (CRD parsers) and pkg/k8s/types (watcher
// internals) both reference the sentinel and can't import each other.
// Operators see it as `debounceInterval: "0"` on the CRD; the parser
// returns the negative sentinel so it survives WatcherConfig.SetDefaults
// (which would otherwise rewrite zero to DefaultDebounceInterval).
func TestDefaultsConsistency_DebounceImmediateShared(t *testing.T) {
	assert.Equal(t,
		types.DebounceImmediate,
		config.DebounceImmediate,
		"DebounceImmediate sentinel must stay equal across pkg/core/config and pkg/k8s/types; update both at the same time")
}
