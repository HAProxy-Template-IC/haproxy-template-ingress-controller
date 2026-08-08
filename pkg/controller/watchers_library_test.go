package controller

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/k8s/types"

	"k8s.io/apimachinery/pkg/runtime/schema"
)

// watcher.New validates its config and returns an error, which setupConfigWatchers
// turns into a failed iteration — visible only as a controller that never becomes
// ready. An omitted IndexBy shipped exactly that: every cluster suite timed out
// after six minutes with "pod present but not Ready", and no offline gate saw it.
func TestLibraryWatcherConfigIsValid(t *testing.T) {
	cfg := libraryWatcherConfig(
		schema.GroupVersionResource{Group: "haproxy-haptic.org", Version: "v1alpha1", Resource: "haproxytemplatelibraries"},
		"haptic",
		func(types.Store) {},
	)

	require.NoError(t, cfg.Validate(), "the library watcher config must satisfy watcher.New")

	assert.NotEmpty(t, cfg.IndexBy, "IndexBy is required and has no default")
	assert.NotNil(t, cfg.OnChange, "OnChange is required")
	assert.NotNil(t, cfg.OnSyncComplete,
		"OnSyncComplete must fire so a namespace with no libraries still delivers the empty snapshot")
}
