package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeDataplane is a minimal Dataplane API: it answers version detection,
// reports a fixed configured version, and lets the test script what each
// force_reload raw push returns while recording the version each one carried.
type fakeDataplane struct {
	// reportedVersion is what GET /configuration/version returns.
	reportedVersion int64
	// pushStatus is consumed in order, one entry per push.
	pushStatus []int

	mu       sync.Mutex
	pushes   []int64
	pushSeen int
}

func (f *fakeDataplane) start(t *testing.T) *DataplaneClient {
	t.Helper()

	// httptest binds loopback, so this never collides on a port and never
	// raises a host firewall prompt.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/info"):
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(VersionInfo{
				API: struct {
					Version string `json:"version"`
				}{Version: "v3.2.6 87ad0bcf"},
			})

		case strings.HasSuffix(r.URL.Path, "/configuration/version"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(strconv.FormatInt(f.reportedVersion, 10)))

		case strings.HasSuffix(r.URL.Path, "/configuration/raw") && r.Method == http.MethodPost:
			v, _ := strconv.ParseInt(r.URL.Query().Get("version"), 10, 64)

			f.mu.Lock()
			f.pushes = append(f.pushes, v)
			status := http.StatusAccepted
			if f.pushSeen < len(f.pushStatus) {
				status = f.pushStatus[f.pushSeen]
			}
			f.pushSeen++
			f.mu.Unlock()

			if status == http.StatusConflict {
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"code":409,"message":"version mismatch, transaction version: 3, configured version: 1"}`))
				return
			}
			w.Header().Set("Reload-ID", "reload-1")
			w.WriteHeader(status)

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)

	c, err := New(context.Background(), &Config{BaseURL: server.URL, Username: "u", Password: "p"})
	require.NoError(t, err)
	return c
}

func (f *fakeDataplane) pushedVersions() []int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int64(nil), f.pushes...)
}

// TestPushRawConfiguration_RetriesOnceAfterVersionConflict pins fix 4 of #112.
//
// A concurrent skip_version bypass on the SAME endpoint resets the dataplane's
// configured version to the headerless sentinel. The structural push that would
// activate the pending render then 409s on its now-stale version, the deploy
// fails, and the render sits parked on disk with no reload — listeners that
// never come up while HAPTIC reports convergence.
//
// Re-resolving and retrying once turns that known interleaving into a landed
// reload. The lock is not weakened: the retry carries the freshly read version,
// so a writer that moves it again still loses.
func TestPushRawConfiguration_RetriesOnceAfterVersionConflict(t *testing.T) {
	fake := &fakeDataplane{
		reportedVersion: 1, // what the bypass left behind
		pushStatus:      []int{http.StatusConflict, http.StatusAccepted},
	}
	c := fake.start(t)

	reloadID, err := c.PushRawConfiguration(context.Background(), "global\n", 3)
	require.NoError(t, err, "a version conflict on the same endpoint must be retried, not left stranded")
	assert.Equal(t, "reload-1", reloadID,
		"the retry's reload must be reported, otherwise the deploy is not credited with activating")

	pushes := fake.pushedVersions()
	require.Len(t, pushes, 2, "exactly one retry")
	assert.Equal(t, int64(3), pushes[0], "first push carries the caller's version")
	assert.Equal(t, int64(1), pushes[1], "retry carries the re-resolved version, so the lock still means something")
}

// TestPushRawConfiguration_NoRetryWhenVersionUnchanged is the other half: when
// the dataplane reports the version that was just pushed, the conflict is not
// the bypass interleaving and repeating the identical request would be futile.
func TestPushRawConfiguration_NoRetryWhenVersionUnchanged(t *testing.T) {
	fake := &fakeDataplane{
		reportedVersion: 3,
		pushStatus:      []int{http.StatusConflict, http.StatusAccepted},
	}
	c := fake.start(t)

	_, err := c.PushRawConfiguration(context.Background(), "global\n", 3)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "409")
	assert.Len(t, fake.pushedVersions(), 1,
		"no retry when the reported version equals the one already pushed")
}

// TestPushRawConfiguration_NoRetryWhenAnotherWriterOwnsTheVersion is the guard
// against the retry becoming a silent overwrite.
//
// The retry is justified by ONE interleaving HAPTIC creates against itself: a
// skip_version bypass resetting the version to the headerless sentinel, where
// nothing versioned wrote the file and there is no other content to lose. A
// conflict reporting any OTHER version means a deliberate writer owns the
// config; re-pushing at their version would clobber it and report success.
// Fail instead — a failed deploy is recoverable, a silent overwrite is not.
func TestPushRawConfiguration_NoRetryWhenAnotherWriterOwnsTheVersion(t *testing.T) {
	fake := &fakeDataplane{
		reportedVersion: 7, // someone bumped it deliberately, not the sentinel
		pushStatus:      []int{http.StatusConflict, http.StatusAccepted},
	}
	c := fake.start(t)

	_, err := c.PushRawConfiguration(context.Background(), "global\n", 3)
	require.Error(t, err, "a conflict against a real writer's version must not be retried")
	assert.Contains(t, err.Error(), "another writer owns this config")
	assert.Len(t, fake.pushedVersions(), 1, "no second push — their content must survive")
}

// TestPushRawConfiguration_ConflictRetryStillFailsOnSecondConflict keeps the
// retry bounded: a second conflict is a real one and must surface.
func TestPushRawConfiguration_ConflictRetryStillFailsOnSecondConflict(t *testing.T) {
	fake := &fakeDataplane{
		reportedVersion: 1,
		pushStatus:      []int{http.StatusConflict, http.StatusConflict},
	}
	c := fake.start(t)

	_, err := c.PushRawConfiguration(context.Background(), "global\n", 3)
	require.Error(t, err, "the retry must not loop; a second conflict fails the push")
	assert.Contains(t, err.Error(), "409")
	assert.Len(t, fake.pushedVersions(), 2, "one retry only")
}
