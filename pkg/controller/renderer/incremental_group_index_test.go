// Copyright 2026 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package renderer

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	iradix "github.com/hashicorp/go-immutable-radix/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/haproxy-haptic/haptic/pkg/httpstore"
	"gitlab.com/haproxy-haptic/haptic/pkg/rendercontent"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

func mustIncrementalGroupOutput(tb testing.TB, index *incrementalGroupIndex, component string) string {
	tb.Helper()
	output, err := index.output(component)
	require.NoError(tb, err)
	return output
}

func mustIncrementalGroupOutputContent(
	tb testing.TB,
	index *incrementalGroupIndex,
	component string,
) rendercontent.Output {
	tb.Helper()
	output, err := index.outputContent(component)
	require.NoError(tb, err)
	return output
}

func mustIncrementalGroupEvents(tb testing.TB, index *incrementalGroupIndex) []templating.RenderedEvent {
	tb.Helper()
	events, err := index.renderedEvents()
	require.NoError(tb, err)
	return events
}

func mustIncrementalGroupHTTP(tb testing.TB, index *incrementalGroupIndex) []incrementalHTTPEffect {
	tb.Helper()
	effects, err := index.httpEffects()
	require.NoError(tb, err)
	return effects
}

func TestIncrementalGroupIndexMatchesCanonicalAssembly(t *testing.T) {
	instances := []incrementalInstanceResult{
		{component: "210-grpc", source: "routes", namespace: "default", name: "b", result: uniqueResult(t, "backends", "shared", "grpc\n")},
		{component: "200-http", source: "routes", namespace: "default", name: "z", result: uniqueResult(t, "backends", "shared", "http-z\n")},
		{component: "200-http", source: "routes", namespace: "default", name: "a", result: uniqueResult(t, "backends", "shared", "http-a\n")},
		{component: "210-grpc", source: "routes", namespace: "default", name: "a", result: uniqueResult(t, "backends", "grpc", "grpc-only\n")},
	}

	index := newIncrementalGroupIndex()
	for item := range slices.Values(instances) {
		var err error
		index, err = index.replace(&item, nil)
		require.NoError(t, err)
	}
	assertIncrementalGroupOutput(t, index, instances, "200-http", "210-grpc")

	before := index
	instances[2].result = incrementalComponentResult{}
	var err error
	index, err = index.replace(&instances[2], nil)
	require.NoError(t, err)
	assertIncrementalGroupOutput(t, index, instances, "200-http", "210-grpc")
	assert.Equal(t, "http-a\n", mustIncrementalGroupOutput(t, before, "200-http"))

	index, err = index.remove("200-http", "routes", "default", "z")
	require.NoError(t, err)
	instances = slices.Delete(instances, 1, 2)
	assertIncrementalGroupOutput(t, index, instances, "200-http", "210-grpc")
}

func TestIncrementalGroupIndexOrdersSourcesCanonically(t *testing.T) {
	instances := []incrementalInstanceResult{
		{component: "component", source: "z-routes", namespace: "default", name: "a", result: incrementalComponentResult{Text: "z\n"}},
		{component: "component", source: "a-routes", namespace: "default", name: "z", result: incrementalComponentResult{Text: "a\n"}},
	}
	index := newIncrementalGroupIndex()
	for _, instance := range instances {
		var err error
		index, err = index.replace(&instance, nil)
		require.NoError(t, err)
	}
	assert.Equal(t, "a\nz\n", mustIncrementalGroupOutput(t, index, "component"))
}

func TestIncrementalGroupIndexPreservesExactOutputRoots(t *testing.T) {
	winner := incrementalInstanceResult{
		component: "component", source: "routes", namespace: "default", name: "a",
		result: uniqueResult(t, "cell", "shared", "winner\n"),
	}
	loser := incrementalInstanceResult{
		component: "component", source: "routes", namespace: "default", name: "z",
		result: uniqueResult(t, "cell", "shared", "loser\n"),
	}
	other := incrementalInstanceResult{
		component: "other", source: "routes", namespace: "default", name: "a",
		result: incrementalComponentResult{Text: "other\n"},
	}
	index, err := newIncrementalGroupIndex().replace(&winner, nil)
	require.NoError(t, err)
	index, err = index.replace(&loser, nil)
	require.NoError(t, err)
	before := mustIncrementalGroupOutputContent(t, index, "component")

	index, err = index.replace(&loser, nil)
	require.NoError(t, err)
	assertSameOutputRoot(t, before, mustIncrementalGroupOutputContent(t, index, "component"))

	loser.result = uniqueResult(t, "cell", "shared", "changed-loser\n")
	index, err = index.replace(&loser, nil)
	require.NoError(t, err)
	assertSameOutputRoot(t, before, mustIncrementalGroupOutputContent(t, index, "component"))

	index, err = index.replace(&other, nil)
	require.NoError(t, err)
	assertSameOutputRoot(t, before, mustIncrementalGroupOutputContent(t, index, "component"))

	winner.result = uniqueResult(t, "cell", "shared", "changed-winner\n")
	index, err = index.replace(&winner, nil)
	require.NoError(t, err)
	after := mustIncrementalGroupOutputContent(t, index, "component")
	assertDifferentOutputRoot(t, before, after)
	assert.Equal(t, "winner\n", mustOutputString(t, before))
	assert.Equal(t, "changed-winner\n", mustOutputString(t, after))
}

func TestIncrementalGroupIndexAcceptsCopiedNestedOutputHandle(t *testing.T) {
	instance := incrementalInstanceResult{
		component: "component", source: "routes", namespace: "default", name: "route",
		result: incrementalComponentResult{Text: "safe\n"},
	}
	index, err := newIncrementalGroupIndex().replace(&instance, nil)
	require.NoError(t, err)
	output := mustIncrementalGroupOutputContent(t, index, instance.component)
	copied := output
	txn := index.outputs.Txn()
	txn.Insert([]byte(instance.component), incrementalComponentChunks{output: copied})
	poisoned := *index
	poisoned.outputs = txn.Commit()
	poisoned.authenticate()

	got, err := poisoned.output(instance.component)
	require.NoError(t, err)
	assert.Equal(t, "safe\n", got)
}

func TestIncrementalGroupIndexAggregatesEffects(t *testing.T) {
	eventA := templating.RenderedEvent{
		Namespace: "default", Name: "a", APIVersion: "example.test/v1", Kind: "Route",
		Type: templating.EventTypeNormal, Reason: "Accepted", Message: "accepted",
	}
	eventZ := eventA
	eventZ.Name = "z"
	httpA := incrementalHTTPEffect{inputID: 1, snapshot: httpstore.ContentSnapshot{URL: "https://a.test", Content: "a", Found: true}}
	httpZ := incrementalHTTPEffect{inputID: 2, snapshot: httpstore.ContentSnapshot{URL: "https://z.test", Content: "z", Found: true}}
	first := incrementalInstanceResult{
		component: "component", source: "routes", namespace: "default", name: "a",
		result: incrementalComponentResult{Events: []templating.RenderedEvent{eventZ, eventA}},
	}
	second := incrementalInstanceResult{
		component: "component", source: "routes", namespace: "default", name: "z",
		result: incrementalComponentResult{Events: []templating.RenderedEvent{eventA}},
	}

	index, err := newIncrementalGroupIndex().replace(&first, []incrementalHTTPEffect{httpZ, httpA})
	require.NoError(t, err)
	index, err = index.replace(&second, []incrementalHTTPEffect{httpA})
	require.NoError(t, err)
	assert.Equal(t, []templating.RenderedEvent{eventA, eventZ}, mustIncrementalGroupEvents(t, index))
	assert.Equal(t, []incrementalHTTPEffect{httpA, httpZ}, mustIncrementalGroupHTTP(t, index))

	conflict := httpA
	conflict.snapshot.Content = "different"
	unchanged := index
	_, err = index.replace(&second, []incrementalHTTPEffect{conflict})
	require.ErrorContains(t, err, "conflicting snapshots")
	assert.Equal(t, []incrementalHTTPEffect{httpA, httpZ}, mustIncrementalGroupHTTP(t, unchanged))

	index, err = index.remove(first.component, first.source, first.namespace, first.name)
	require.NoError(t, err)
	assert.Equal(t, []templating.RenderedEvent{eventA}, mustIncrementalGroupEvents(t, index))
	assert.Equal(t, []incrementalHTTPEffect{httpA}, mustIncrementalGroupHTTP(t, index))
}

func TestIncrementalGroupIndexKeepsExactEventTuplesAndHTTPWinnerSemantics(t *testing.T) {
	firstEvent := templating.RenderedEvent{
		Namespace: "default", Name: "route", APIVersion: "example.test/v1", Kind: "Route",
		Type: templating.EventTypeNormal, Reason: "Accepted/a", Message: "b",
	}
	lastEvent := firstEvent
	lastEvent.Reason = "Accepted"
	lastEvent.Message = "a/b"
	store := httpstore.New(nil, 0)
	store.LoadFixture("https://a.test", "same")
	firstHTTP := incrementalHTTPEffect{
		inputID:  1,
		snapshot: store.AcceptedSnapshot("https://a.test", httpstore.SourceDescriptor{}),
	}
	store.LoadFixture("https://a.test", "intermediate")
	store.LoadFixture("https://a.test", "same")
	lastHTTP := incrementalHTTPEffect{
		inputID:  1,
		snapshot: store.AcceptedSnapshot("https://a.test", httpstore.SourceDescriptor{}),
	}
	require.False(t, sameHTTPSnapshot(&firstHTTP.snapshot, &lastHTTP.snapshot))
	require.True(t, sameHTTPReusableSnapshot(&firstHTTP.snapshot, &lastHTTP.snapshot))
	first := incrementalInstanceResult{
		component: "component", source: "routes", namespace: "default", name: "a",
		result: incrementalComponentResult{Events: []templating.RenderedEvent{firstEvent}},
	}
	last := incrementalInstanceResult{
		component: "component", source: "routes", namespace: "default", name: "z",
		result: incrementalComponentResult{Events: []templating.RenderedEvent{lastEvent}},
	}

	index, err := newIncrementalGroupIndex().replace(&first, []incrementalHTTPEffect{firstHTTP})
	require.NoError(t, err)
	index, err = index.replace(&last, []incrementalHTTPEffect{lastHTTP})
	require.NoError(t, err)
	assert.Equal(t, []templating.RenderedEvent{lastEvent, firstEvent}, mustIncrementalGroupEvents(t, index))
	assert.Equal(t, []incrementalHTTPEffect{lastHTTP}, mustIncrementalGroupHTTP(t, index))

	otherStore := httpstore.New(nil, 0)
	otherStore.LoadFixture("https://a.test", "same")
	differentDescriptor, err := httpstore.DescribeSource(httpstore.FetchOptions{Critical: true}, nil)
	require.NoError(t, err)
	conflicts := []struct {
		name     string
		snapshot httpstore.ContentSnapshot
	}{
		{name: "URL", snapshot: func() httpstore.ContentSnapshot {
			value := lastHTTP.snapshot
			value.URL = "https://different.test"
			return value
		}()},
		{name: "descriptor", snapshot: func() httpstore.ContentSnapshot {
			value := lastHTTP.snapshot
			value.Descriptor = differentDescriptor
			return value
		}()},
		{name: "content", snapshot: func() httpstore.ContentSnapshot {
			value := lastHTTP.snapshot
			value.Content = "different"
			return value
		}()},
		{name: "found state", snapshot: func() httpstore.ContentSnapshot {
			value := lastHTTP.snapshot
			value.Found = false
			return value
		}()},
		{name: "cacheability", snapshot: func() httpstore.ContentSnapshot {
			value := lastHTTP.snapshot
			value.Cacheable = false
			return value
		}()},
		{name: "token observation", snapshot: func() httpstore.ContentSnapshot {
			value := lastHTTP.snapshot
			value.Observation = firstHTTP.snapshot.Observation
			return value
		}()},
		{name: "observation token", snapshot: func() httpstore.ContentSnapshot {
			value := lastHTTP.snapshot
			value.Token = firstHTTP.snapshot.Token
			return value
		}()},
		{name: "observation watermark", snapshot: func() httpstore.ContentSnapshot {
			value := lastHTTP.snapshot
			value.Watermark = 0
			return value
		}()},
		{
			name:     "store source",
			snapshot: otherStore.AcceptedSnapshot("https://a.test", httpstore.SourceDescriptor{}),
		},
	}
	for _, conflict := range conflicts {
		t.Run(conflict.name, func(t *testing.T) {
			_, err := index.replace(&last, []incrementalHTTPEffect{{
				inputID:  1,
				snapshot: conflict.snapshot,
			}})
			require.ErrorContains(t, err, "conflicting snapshots")
			assert.Equal(t, []incrementalHTTPEffect{lastHTTP}, mustIncrementalGroupHTTP(t, index))
		})
	}
}

func TestIncrementalGroupIndexRejectsDifferentInitialCandidates(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("same"))
	}))
	t.Cleanup(server.Close)
	store := httpstore.New(nil, 0)
	reconciled, err := store.ReconcileSource(server.URL, httpstore.FetchOptions{Critical: true}, nil)
	require.NoError(t, err)
	firstSnapshot, _, err := store.PrepareInitialSnapshot(t.Context(), server.URL, reconciled.State)
	require.NoError(t, err)
	lastSnapshot, _, err := store.PrepareInitialSnapshot(t.Context(), server.URL, reconciled.State)
	require.NoError(t, err)
	require.Equal(t, httpstore.SnapshotInitialCandidate, firstSnapshot.Token.Kind())
	require.Equal(t, httpstore.SnapshotInitialCandidate, lastSnapshot.Token.Kind())
	require.True(t, sameHTTPSemanticValue(&firstSnapshot, &lastSnapshot))
	require.False(t, sameHTTPSnapshot(&firstSnapshot, &lastSnapshot))
	require.False(t, sameHTTPReusableSnapshot(&firstSnapshot, &lastSnapshot))

	first := incrementalInstanceResult{component: "component", source: "routes", name: "a"}
	last := incrementalInstanceResult{component: "component", source: "routes", name: "z"}
	index, err := newIncrementalGroupIndex().replace(&first, []incrementalHTTPEffect{{
		inputID: 1, snapshot: firstSnapshot,
	}})
	require.NoError(t, err)
	_, err = index.replace(&last, []incrementalHTTPEffect{{inputID: 1, snapshot: lastSnapshot}})
	require.ErrorContains(t, err, "conflicting snapshots")
}

func TestIncrementalGroupIndexRejectsInvalidOrInconsistentUpdates(t *testing.T) {
	instance := incrementalInstanceResult{
		component: "component", source: "routes", namespace: "default", name: "route",
		result: uniqueResult(t, "cell", "key", "value\n"),
	}
	index, err := newIncrementalGroupIndex().replace(&instance, nil)
	require.NoError(t, err)

	invalid := instance
	invalid.result.Text = "mixed"
	_, err = index.replace(&invalid, nil)
	require.ErrorContains(t, err, "cannot mix text")
	assert.Equal(t, "value\n", mustIncrementalGroupOutput(t, index, "component"))

	poisoned := *index
	poisoned.contributors = iradix.New[*iradix.Tree[incrementalIndexedContribution]]()
	_, err = poisoned.replace(&instance, nil)
	require.ErrorContains(t, err, "authentication seal")
	assert.Equal(t, "value\n", mustIncrementalGroupOutput(t, index, "component"))
}

func TestIncrementalGroupIndexRandomizedDifferential(t *testing.T) {
	random := rand.New(rand.NewSource(187))
	index := newIncrementalGroupIndex()
	instances := make(map[string]incrementalInstanceResult)
	components := []string{"100-first", "200-second", "300-third"}

	for operation := range 500 {
		id := incrementalGroupInstanceID{
			component: components[random.Intn(len(components))],
			source:    []string{"a", "z", "\x00source"}[random.Intn(3)],
			namespace: []string{"", "default", "other"}[random.Intn(3)],
			name:      fmt.Sprintf("route-%02d", random.Intn(30)),
		}
		key := string(incrementalGroupInstanceKey(id))
		var err error
		if random.Intn(4) == 0 {
			index, err = index.remove(id.component, id.source, id.namespace, id.name)
			delete(instances, key)
		} else {
			instance := incrementalInstanceResult{
				component: id.component,
				source:    id.source,
				namespace: id.namespace,
				name:      id.name,
				result:    randomIncrementalComponentResult(random, operation),
			}
			index, err = index.replace(&instance, nil)
			instances[key] = instance
		}
		require.NoError(t, err)
		values := make([]incrementalInstanceResult, 0, len(instances))
		for _, instance := range instances {
			values = append(values, instance)
		}
		assertIncrementalGroupOutput(t, index, values, components...)
	}
}

func TestIncrementalOrderedTuplePreservesStringOrder(t *testing.T) {
	values := []string{"", "\x00", "\x00\x00", "\x00a", "a", "a\x00", "aa", "b", "\xff"}
	for _, left := range values {
		for _, right := range values {
			assert.Equal(t,
				strings.Compare(left, right),
				bytes.Compare(incrementalOrderedTuple(left), incrementalOrderedTuple(right)),
				"%q compared with %q", left, right,
			)
		}
	}
}

func TestIncrementalGroupLocationKeyForInstanceKeyMatchesCanonicalEncoding(t *testing.T) {
	id := incrementalGroupInstanceID{
		component: "component\x00name",
		source:    "source\xff",
		namespace: "namespace\x00",
		name:      "name",
	}
	instanceKey := incrementalGroupInstanceKey(id)
	for _, index := range []uint64{0, 1, 1<<32 + 7, ^uint64(0)} {
		want := append(referenceIncrementalOrderedTuple(id.component, id.source, id.namespace, id.name), make([]byte, 8)...)
		binary.BigEndian.PutUint64(want[len(want)-8:], index)
		got := incrementalGroupLocationKeyForInstanceKey(instanceKey, index)
		require.Equal(t, want, got)
		got[0] ^= 0xff
		require.Equal(t, incrementalGroupInstanceKey(id), instanceKey)
	}
}

func TestIncrementalOrderedTuplePreservesExactEscapedBytes(t *testing.T) {
	parts := []string{"\x00a", "", "\xff\x00", "nested\x00\x00tuple"}
	require.Equal(t, referenceIncrementalOrderedTuple(parts...), incrementalOrderedTuple(parts...))
	for first := range 256 {
		require.Equal(
			t,
			referenceIncrementalOrderedTuple(string([]byte{byte(first)})),
			incrementalOrderedTuple(string([]byte{byte(first)})),
		)
		for second := range 256 {
			value := string([]byte{byte(first), byte(second)})
			require.Equal(t, referenceIncrementalOrderedTuple(value), incrementalOrderedTuple(value))
			require.Equal(
				t,
				referenceIncrementalOrderedTuple(value[:1], value[1:]),
				incrementalOrderedTuple(value[:1], value[1:]),
			)
		}
	}
	for length := range 1025 {
		value := make([]byte, length)
		for index := range value {
			value[index] = byte(index*131 + length*17)
		}
		first := length / 3
		second := first + (length-first)/2
		generated := []string{string(value[:first]), string(value[first:second]), string(value[second:])}
		require.Equal(
			t,
			referenceIncrementalOrderedTuple(generated...),
			incrementalOrderedTuple(generated...),
		)
	}

	allocations := testing.AllocsPerRun(1000, func() {
		incrementalOrderedTupleTestSink = incrementalOrderedTuple(parts...)
	})
	require.Equal(t, float64(1), allocations)
}

var incrementalOrderedTupleTestSink []byte

func referenceIncrementalOrderedTuple(parts ...string) []byte {
	encoded := make([]byte, 0)
	for _, part := range parts {
		for index := range len(part) {
			if part[index] == 0 {
				encoded = append(encoded, 0, 0xff)
			} else {
				encoded = append(encoded, part[index])
			}
		}
		encoded = append(encoded, 0, 0)
	}
	return encoded
}

func BenchmarkIncrementalOrderedTupleEscaped(b *testing.B) {
	parts := []string{"cell\x00rank", "component\x00source\x00namespace\x00name", "value"}
	b.Run("exact-sized", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			incrementalOrderedTupleTestSink = incrementalOrderedTuple(parts...)
		}
	})
	b.Run("previous-under-sized", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			incrementalOrderedTupleTestSink = previousIncrementalOrderedTuple(parts...)
		}
	})
}

func previousIncrementalOrderedTuple(parts ...string) []byte {
	length := 0
	for _, part := range parts {
		length += len(part) + 2
	}
	encoded := make([]byte, 0, length)
	for _, part := range parts {
		for index := range len(part) {
			if part[index] == 0 {
				encoded = append(encoded, 0, 0xff)
			} else {
				encoded = append(encoded, part[index])
			}
		}
		encoded = append(encoded, 0, 0)
	}
	return encoded
}

func BenchmarkIncrementalGroupIndexOneChange(b *testing.B) {
	instances, index := incrementalGroupBenchmarkFixture(b, 3000)
	b.Run("full-assembly", func(b *testing.B) {
		benchmarkIncrementalFullAssembly(b, instances)
	})
	b.Run("persistent-index", func(b *testing.B) {
		benchmarkIncrementalPersistentIndex(b, instances, index)
	})
	b.Run("output-handle", func(b *testing.B) {
		benchmarkIncrementalOutputHandle(b, index)
	})
	b.Run("persistent-update", func(b *testing.B) {
		benchmarkIncrementalPersistentUpdate(b, instances, index)
	})
}

func benchmarkIncrementalOutputHandle(b *testing.B, index *incrementalGroupIndex) {
	b.Helper()
	b.ReportAllocs()
	for range b.N {
		output, err := index.outputContent("component")
		if err != nil {
			b.Fatal(err)
		}
		incrementalGroupOutputSink = output
	}
}

func incrementalGroupBenchmarkFixture(
	b *testing.B,
	count int,
) ([]incrementalInstanceResult, *incrementalGroupIndex) {
	b.Helper()
	instances := make([]incrementalInstanceResult, count)
	index := newIncrementalGroupIndex()
	for item := range count {
		instances[item] = incrementalInstanceResult{
			component: "component",
			source:    "routes",
			namespace: "default",
			name:      fmt.Sprintf("route-%06d", item),
			result:    incrementalComponentResult{Text: fmt.Sprintf("route-%06d=a\n", item)},
		}
		var err error
		index, err = index.replace(&instances[item], nil)
		if err != nil {
			b.Fatal(err)
		}
	}
	return instances, index
}

func benchmarkIncrementalFullAssembly(b *testing.B, instances []incrementalInstanceResult) {
	b.Helper()
	b.ReportAllocs()
	for operation := range b.N {
		instances[0].result.Text = fmt.Sprintf("route-000000=%d\n", operation&1)
		outputs, err := assembleIncrementalGroup(instances)
		if err != nil {
			b.Fatal(err)
		}
		if outputs["component"] == "" {
			b.Fatal("empty output")
		}
	}
}

func benchmarkIncrementalPersistentIndex(
	b *testing.B,
	instances []incrementalInstanceResult,
	index *incrementalGroupIndex,
) {
	b.Helper()
	b.ReportAllocs()
	current := index
	for operation := range b.N {
		instances[0].result.Text = fmt.Sprintf("route-000000=%d\n", operation&1)
		var err error
		current, err = current.replace(&instances[0], nil)
		if err != nil {
			b.Fatal(err)
		}
		if mustIncrementalGroupOutput(b, current, "component") == "" {
			b.Fatal("empty output")
		}
	}
}

func benchmarkIncrementalPersistentUpdate(
	b *testing.B,
	instances []incrementalInstanceResult,
	index *incrementalGroupIndex,
) {
	b.Helper()
	b.ReportAllocs()
	current := index
	first := instances[0]
	second := first
	first.result.Text = "route-000000=0\n"
	second.result.Text = "route-000000=1\n"
	for operation := range b.N {
		next := first
		if operation&1 != 0 {
			next = second
		}
		var err error
		current, err = current.replace(&next, nil)
		if err != nil {
			b.Fatal(err)
		}
	}
	incrementalGroupIndexSink = current
}

var incrementalGroupIndexSink *incrementalGroupIndex
var incrementalGroupOutputSink rendercontent.Output

func mustOutputString(t *testing.T, output rendercontent.Output) string {
	t.Helper()
	value, err := output.String()
	require.NoError(t, err)
	return value
}

func assertSameOutputRoot(t *testing.T, left, right rendercontent.Output) {
	t.Helper()
	same, err := left.SameRoot(right)
	require.NoError(t, err)
	assert.True(t, same)
}

func assertDifferentOutputRoot(t *testing.T, left, right rendercontent.Output) {
	t.Helper()
	same, err := left.SameRoot(right)
	require.NoError(t, err)
	assert.False(t, same)
}

func assertIncrementalGroupOutput(
	t *testing.T,
	index *incrementalGroupIndex,
	instances []incrementalInstanceResult,
	components ...string,
) {
	t.Helper()
	want, err := assembleIncrementalGroup(instances)
	require.NoError(t, err)
	for _, component := range components {
		assert.Equal(t, want[component], mustIncrementalGroupOutput(t, index, component), component)
	}
}

func randomIncrementalComponentResult(random *rand.Rand, operation int) incrementalComponentResult {
	if random.Intn(3) == 0 {
		return incrementalComponentResult{Text: fmt.Sprintf("text-%03d\n", operation)}
	}
	values := make([]incrementalContribution, random.Intn(4))
	for index := range values {
		values[index] = incrementalContribution{
			Cell:  fmt.Sprintf("cell-%d", random.Intn(3)),
			Key:   fmt.Sprintf("key-%d", random.Intn(8)),
			Value: fmt.Sprintf("value-%03d-%d\n", operation, index),
		}
	}
	return incrementalComponentResult{Unique: values}
}
