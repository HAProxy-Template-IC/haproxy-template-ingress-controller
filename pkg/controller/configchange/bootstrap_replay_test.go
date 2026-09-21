package configchange

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
	"gitlab.com/haproxy-haptic/haptic/pkg/controller/testutil"
	coreconfig "gitlab.com/haproxy-haptic/haptic/pkg/core/config"
)

func TestBootstrapPreservesValidationReplay(t *testing.T) {
	for _, source := range []string{"startup snapshot", "watcher verdict", "active restore"} {
		t.Run(source, func(t *testing.T) {
			bus, logger := testutil.NewTestBusAndLogger()
			handler := NewConfigChangeHandler(bus, logger, make(chan *ReloadRequest, 1), nil, 0)
			config := &coreconfig.Config{}
			htc := newHTC()
			setValidatedCondition(&htc.Status, metav1.ConditionFalse, reasonLoadGateFailed,
				"old controller cannot compile the upgraded templates", testGeneration)
			updater, client := newStatusUpdaterFixture(t, htc)
			handler.SetInitialSnapshot(&ValidatedSnapshot{
				Config: config, TemplateConfig: htc, ConfigVersion: "config=3", CredentialsVersion: "secret=2",
			})
			if source == "watcher verdict" {
				handler.handleConfigValidated(events.NewConfigValidatedEvent(config, htc, "config=3", "secret=2"))
			}
			if source == "active restore" {
				active, _ := handler.configReplayer.Get()
				handler.configReplayer.Cache(newActiveSnapshotRestore(active))
			}
			handler.handleConfigValidated(events.NewConfigValidatedEvent(config, htc, syntheticBootstrapVersion, "initial"))
			observed := bus.Subscribe("validation-replay-test", 10)
			bus.Start()
			handler.handleBecameLeader(events.NewBecameLeaderEvent("new-controller"))
			replayed := testutil.WaitForEvent[*events.ConfigValidatedEvent](t, observed, testutil.LongTimeout)
			require.Equal(t, "config=3", replayed.Version)
			assert.Equal(t, "secret=2", replayed.SecretVersion)
			assert.Equal(t, source == "active restore", replayed.ActiveSnapshotRestore)
			updater.handleConfigValidated(t.Context(), replayed)
			status := getStatus(t, client)
			if source == "active restore" {
				assert.Equal(t, metav1.ConditionFalse, status.Conditions[0].Status)
			} else {
				assert.Equal(t, metav1.ConditionTrue, status.Conditions[0].Status)
			}
		})
	}
}
