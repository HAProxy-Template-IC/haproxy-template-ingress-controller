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
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIncrementalGroupMemoReusesOnlyUnchangedCells(t *testing.T) {
	first := incrementalInstanceResult{
		component: "producer", source: "routes", namespace: "default", name: "first",
		result: publishedResult(t, memoPublicationValue(t, "first", "value", map[string]any{
			"nested": map[string]any{"value": "first"},
		})),
	}
	second := incrementalInstanceResult{
		component: "producer", source: "routes", namespace: "default", name: "second",
		result: publishedResult(t, memoPublicationValue(t, "second", "value", map[string]any{
			"nested": map[string]any{"value": "before"},
		})),
	}
	index := newIncrementalGroupIndex()
	var err error
	index, err = index.replace(&first, nil)
	require.NoError(t, err)
	index, err = index.replace(&second, nil)
	require.NoError(t, err)

	firstValues, firstCertificate, err := index.certifiedPublishedValues("first")
	require.NoError(t, err)
	secondValues, secondCertificate, err := index.certifiedPublishedValues("second")
	require.NoError(t, err)
	require.Len(t, firstValues, 1)
	require.Len(t, secondValues, 1)
	assert.Equal(t, 2, incrementalGroupMemoEntryCount(index.memo, false))

	second.result = publishedResult(t, memoPublicationValue(t, "second", "value", map[string]any{
		"nested": map[string]any{"value": "after"},
	}))
	updated, err := index.replace(&second, nil)
	require.NoError(t, err)
	require.NotSame(t, index.memo, updated.memo)
	require.Same(t, index.memo.authority, updated.memo.authority)

	unchangedValues, unchangedCertificate, err := updated.certifiedPublishedValues("first")
	require.NoError(t, err)
	require.Len(t, unchangedValues, 1)
	assert.Same(t, &firstValues[0], &unchangedValues[0])
	assert.Same(t, firstCertificate, unchangedCertificate)

	changedValues, changedCertificate, err := updated.certifiedPublishedValues("second")
	require.NoError(t, err)
	require.Len(t, changedValues, 1)
	assert.NotSame(t, &secondValues[0], &changedValues[0])
	assert.NotSame(t, secondCertificate, changedCertificate)
	assert.Equal(t, "after", changedValues[0].(map[string]any)["nested"].(map[string]any)["value"])
	assert.Equal(t, 2, incrementalGroupMemoEntryCount(updated.memo, false))
}

func TestIncrementalGroupMemoPreservesEqualReplacement(t *testing.T) {
	instance := incrementalInstanceResult{
		component: "producer", source: "routes", namespace: "default", name: "route",
		result: publishedResult(t, memoPublicationValue(t, "values", "key", map[string]any{"value": "same"})),
	}
	index, err := newIncrementalGroupIndex().replace(&instance, nil)
	require.NoError(t, err)
	values, certificate, err := index.certifiedPublishedValues("values")
	require.NoError(t, err)
	require.Len(t, values, 1)
	projection, exists := index.publicationWinnersByLocation.Root().Get(incrementalOrderedTuple("values"))
	require.True(t, exists)

	updated, err := index.replace(&instance, nil)
	require.NoError(t, err)
	updatedProjection, exists := updated.publicationWinnersByLocation.Root().Get(incrementalOrderedTuple("values"))
	require.True(t, exists)
	assert.Same(t, projection.Root(), updatedProjection.Root())

	updatedValues, updatedCertificate, err := updated.certifiedPublishedValues("values")
	require.NoError(t, err)
	require.Len(t, updatedValues, 1)
	assert.Same(t, &values[0], &updatedValues[0])
	assert.Same(t, certificate, updatedCertificate)
}

func TestIncrementalGroupMemoPreservesUnchangedWinner(t *testing.T) {
	winner := incrementalInstanceResult{
		component: "producer", source: "routes", namespace: "default", name: "a",
		result: publishedResult(t, memoPublicationValue(t, "values", "shared", "winner")),
	}
	loser := incrementalInstanceResult{
		component: "producer", source: "routes", namespace: "default", name: "z",
		result: publishedResult(t, memoPublicationValue(t, "values", "shared", "loser-before")),
	}
	index := newIncrementalGroupIndex()
	var err error
	index, err = index.replace(&winner, nil)
	require.NoError(t, err)
	index, err = index.replace(&loser, nil)
	require.NoError(t, err)
	values, certificate, err := index.certifiedPublishedValues("values")
	require.NoError(t, err)
	require.Equal(t, []any{"winner"}, values)

	loser.result = publishedResult(t, memoPublicationValue(t, "values", "shared", "loser-after"))
	updated, err := index.replace(&loser, nil)
	require.NoError(t, err)
	updatedValues, updatedCertificate, err := updated.certifiedPublishedValues("values")
	require.NoError(t, err)
	assert.Same(t, &values[0], &updatedValues[0])
	assert.Same(t, certificate, updatedCertificate)
}

func TestIncrementalGroupMemoRetainsEveryCurrentCell(t *testing.T) {
	const cellCount = 128
	publications := make([]incrementalPublishedValue, 0, cellCount)
	for index := range cellCount {
		cell := fmt.Sprintf("cell-%03d", index)
		publications = append(publications, memoPublicationValue(t, cell, "key", cell))
	}
	instance := incrementalInstanceResult{
		component: "producer", source: "routes", namespace: "default", name: "route",
		result: publishedResult(t, publications...),
	}
	index, err := newIncrementalGroupIndex().replace(&instance, nil)
	require.NoError(t, err)
	values := make([][]any, cellCount)
	certificates := make([]any, cellCount)
	for cellIndex := range cellCount {
		cell := fmt.Sprintf("cell-%03d", cellIndex)
		value, certificate, readErr := index.certifiedPublishedValues(cell)
		require.NoError(t, readErr)
		require.Len(t, value, 1)
		values[cellIndex] = value
		certificates[cellIndex] = certificate
	}
	assert.Equal(t, cellCount, incrementalGroupMemoEntryCount(index.memo, false))
	for cellIndex := range cellCount {
		cell := fmt.Sprintf("cell-%03d", cellIndex)
		value, certificate, readErr := index.certifiedPublishedValues(cell)
		require.NoError(t, readErr)
		assert.Same(t, &values[cellIndex][0], &value[0])
		assert.Same(t, certificates[cellIndex], certificate)
	}
}

func TestIncrementalGroupMemoDoesNotRetainDelimiterVariants(t *testing.T) {
	instance := incrementalInstanceResult{
		component: "producer", source: "routes", namespace: "default", name: "route",
		result: rankedFragmentResult(t,
			incrementalRankedFragment{"documents", "one", "100", "one"},
			incrementalRankedFragment{"documents", "two", "200", "two"},
		),
	}
	index, err := newIncrementalGroupIndex().replace(&instance, nil)
	require.NoError(t, err)
	for delimiterIndex := range 128 {
		delimiter := fmt.Sprintf("\x00%d\x00", delimiterIndex)
		text, readErr := index.rankedFragments("documents", delimiter)
		require.NoError(t, readErr)
		assert.Equal(t, "one"+delimiter+"two", text)
	}
	assert.Equal(t, 1, incrementalGroupMemoEntryCount(index.memo, true))
}

func TestIncrementalGroupMemoConcurrentFill(t *testing.T) {
	instance := incrementalInstanceResult{
		component: "producer", source: "routes", namespace: "default", name: "route",
		result: rankedFragmentResult(t,
			incrementalRankedFragment{"values", "one", "100", "one"},
			incrementalRankedFragment{"values", "two", "200", "two"},
		),
	}
	index, err := newIncrementalGroupIndex().replace(&instance, nil)
	require.NoError(t, err)
	const readers = 64
	values := make([][]any, readers)
	certificates := make([]any, readers)
	texts := make([]string, readers)
	errs := make([]error, readers)
	var wait sync.WaitGroup
	for reader := range readers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			values[reader], certificates[reader], errs[reader] = index.certifiedPublishedValues("values")
			if errs[reader] != nil {
				return
			}
			texts[reader], errs[reader] = index.rankedFragments("values", "\x00")
		}()
	}
	wait.Wait()
	for reader := range readers {
		require.NoError(t, errs[reader])
		require.Len(t, values[reader], 2)
		assert.Equal(t, "one\x00two", texts[reader])
		assert.Same(t, &values[0][0], &values[reader][0])
		assert.Same(t, certificates[0], certificates[reader])
	}
	assert.Equal(t, 1, incrementalGroupMemoEntryCount(index.memo, false))
	assert.Equal(t, 1, incrementalGroupMemoEntryCount(index.memo, true))
}

func TestIncrementalGroupMemoForkedFillsAreIndependent(t *testing.T) {
	publication := incrementalInstanceResult{
		component: "producer", source: "routes", namespace: "default", name: "route",
		result: rankedFragmentResult(t,
			incrementalRankedFragment{"values", "one", "100", "one"},
			incrementalRankedFragment{"values", "two", "200", "two"},
		),
	}
	parent, err := newIncrementalGroupIndex().replace(&publication, nil)
	require.NoError(t, err)
	unrelated := incrementalInstanceResult{
		component: "consumer", source: "routes", namespace: "default", name: "route",
		result: incrementalComponentResult{Text: "unchanged publication generation\n"},
	}
	child, err := parent.replace(&unrelated, nil)
	require.NoError(t, err)
	require.Zero(t, incrementalGroupMemoEntryCount(parent.memo, false))
	require.Zero(t, incrementalGroupMemoEntryCount(child.memo, false))

	indexes := []*incrementalGroupIndex{parent, child}
	values := make([][]any, len(indexes))
	certificates := make([]any, len(indexes))
	texts := make([]string, len(indexes))
	errs := make([]error, len(indexes))
	var wait sync.WaitGroup
	for index := range indexes {
		wait.Add(1)
		go func() {
			defer wait.Done()
			values[index], certificates[index], errs[index] = indexes[index].certifiedPublishedValues("values")
			if errs[index] != nil {
				return
			}
			texts[index], errs[index] = indexes[index].rankedFragments("values", "|")
		}()
	}
	wait.Wait()
	for index := range indexes {
		require.NoError(t, errs[index])
		require.Equal(t, []any{"one", "two"}, values[index])
		assert.Equal(t, "one|two", texts[index])
		assert.Equal(t, 1, incrementalGroupMemoEntryCount(indexes[index].memo, false))
		assert.Equal(t, 1, incrementalGroupMemoEntryCount(indexes[index].memo, true))
	}
	assert.NotSame(t, &values[0][0], &values[1][0])
	assert.NotSame(t, certificates[0], certificates[1])
	assert.NotSame(t, incrementalGroupRankedMemoEntry(t, parent, "values"),
		incrementalGroupRankedMemoEntry(t, child, "values"))
}

type incrementalGroupMemoReadExpectation struct {
	index              *incrementalGroupIndex
	changed            string
	stableValue        *any
	stableCertificate  any
	changedValue       *any
	changedCertificate any
}

func readIncrementalGroupMemoExpectation(expectation *incrementalGroupMemoReadExpectation) error {
	for range 100 {
		stable, certificate, readErr := expectation.index.certifiedPublishedValues("stable")
		if readErr != nil || len(stable) != 1 || stable[0] != "stable" ||
			&stable[0] != expectation.stableValue || certificate != expectation.stableCertificate {
			return fmt.Errorf("stable read: values=%v certificate=%p error=%v", stable, certificate, readErr)
		}
		changed, certificate, readErr := expectation.index.certifiedPublishedValues("changed")
		if readErr != nil || len(changed) != 1 || changed[0] != expectation.changed ||
			&changed[0] != expectation.changedValue || certificate != expectation.changedCertificate {
			return fmt.Errorf("changed read: values=%v certificate=%p error=%v", changed, certificate, readErr)
		}
		text, readErr := expectation.index.rankedFragments("changed", "")
		if readErr != nil || text != expectation.changed {
			return fmt.Errorf("ranked read: text=%q error=%v", text, readErr)
		}
	}
	return nil
}

func TestIncrementalGroupMemoParentAndChildConcurrentReads(t *testing.T) {
	instance := incrementalInstanceResult{
		component: "producer", source: "routes", namespace: "default", name: "route",
		result: rankedFragmentResult(t,
			incrementalRankedFragment{"stable", "value", "100", "stable"},
			incrementalRankedFragment{"changed", "value", "100", "before"},
		),
	}
	parent, err := newIncrementalGroupIndex().replace(&instance, nil)
	require.NoError(t, err)
	stableValues, stableCertificate, err := parent.certifiedPublishedValues("stable")
	require.NoError(t, err)
	changedValues, changedCertificate, err := parent.certifiedPublishedValues("changed")
	require.NoError(t, err)
	_, err = parent.rankedFragments("stable", "")
	require.NoError(t, err)
	_, err = parent.rankedFragments("changed", "")
	require.NoError(t, err)
	parentStableRanked := incrementalGroupRankedMemoEntry(t, parent, "stable")
	parentChangedRanked := incrementalGroupRankedMemoEntry(t, parent, "changed")

	instance.result = rankedFragmentResult(t,
		incrementalRankedFragment{"stable", "value", "100", "stable"},
		incrementalRankedFragment{"changed", "value", "100", "after"},
	)
	child, err := parent.replace(&instance, nil)
	require.NoError(t, err)
	assert.Same(t, incrementalGroupPublishedMemoEntry(t, parent, "stable"),
		incrementalGroupPublishedMemoEntry(t, child, "stable"))
	assert.Same(t, parentStableRanked, incrementalGroupRankedMemoEntry(t, child, "stable"))
	assert.Nil(t, incrementalGroupPublishedMemoEntryIfPresent(child, "changed"))
	assert.Nil(t, incrementalGroupRankedMemoEntryIfPresent(child, "changed"))
	childChangedValues, childChangedCertificate, err := child.certifiedPublishedValues("changed")
	require.NoError(t, err)
	_, err = child.rankedFragments("changed", "")
	require.NoError(t, err)

	expectations := []incrementalGroupMemoReadExpectation{
		{parent, "before", &stableValues[0], stableCertificate, &changedValues[0], changedCertificate},
		{child, "after", &stableValues[0], stableCertificate, &childChangedValues[0], childChangedCertificate},
	}
	errorsFound := make(chan error, 32)
	var wait sync.WaitGroup
	for _, expectation := range expectations {
		for range 16 {
			expectation := expectation
			wait.Add(1)
			go func() {
				defer wait.Done()
				if readErr := readIncrementalGroupMemoExpectation(&expectation); readErr != nil {
					errorsFound <- readErr
				}
			}()
		}
	}
	wait.Wait()
	close(errorsFound)
	for readErr := range errorsFound {
		require.NoError(t, readErr)
	}
	assert.Same(t, parentChangedRanked, incrementalGroupRankedMemoEntry(t, parent, "changed"))
	assert.Equal(t, "before", changedValues[0])
	assert.Equal(t, 2, incrementalGroupMemoEntryCount(parent.memo, false))
	assert.Equal(t, 2, incrementalGroupMemoEntryCount(parent.memo, true))
}

func TestIncrementalGroupMemoSiblingInvalidationsAreIndependent(t *testing.T) {
	instance := incrementalInstanceResult{
		component: "producer", source: "routes", namespace: "default", name: "route",
		result: publishedResult(t,
			memoPublicationValue(t, "left", "key", "left-before"),
			memoPublicationValue(t, "right", "key", "right-before"),
		),
	}
	parent, err := newIncrementalGroupIndex().replace(&instance, nil)
	require.NoError(t, err)
	parentLeft, parentLeftCertificate, err := parent.certifiedPublishedValues("left")
	require.NoError(t, err)
	parentRight, parentRightCertificate, err := parent.certifiedPublishedValues("right")
	require.NoError(t, err)

	leftChanged := instance
	leftChanged.result = publishedResult(t,
		memoPublicationValue(t, "left", "key", "left-after"),
		memoPublicationValue(t, "right", "key", "right-before"),
	)
	leftSibling, err := parent.replace(&leftChanged, nil)
	require.NoError(t, err)
	rightChanged := instance
	rightChanged.result = publishedResult(t,
		memoPublicationValue(t, "left", "key", "left-before"),
		memoPublicationValue(t, "right", "key", "right-after"),
	)
	rightSibling, err := parent.replace(&rightChanged, nil)
	require.NoError(t, err)

	assert.Nil(t, incrementalGroupPublishedMemoEntryIfPresent(leftSibling, "left"))
	assert.Same(t, incrementalGroupPublishedMemoEntry(t, parent, "right"),
		incrementalGroupPublishedMemoEntry(t, leftSibling, "right"))
	assert.Same(t, incrementalGroupPublishedMemoEntry(t, parent, "left"),
		incrementalGroupPublishedMemoEntry(t, rightSibling, "left"))
	assert.Nil(t, incrementalGroupPublishedMemoEntryIfPresent(rightSibling, "right"))

	leftValues, leftCertificate, err := leftSibling.certifiedPublishedValues("left")
	require.NoError(t, err)
	rightValues, rightCertificate, err := rightSibling.certifiedPublishedValues("right")
	require.NoError(t, err)
	require.Equal(t, []any{"left-after"}, leftValues)
	require.Equal(t, []any{"right-after"}, rightValues)
	assert.NotSame(t, &parentLeft[0], &leftValues[0])
	assert.NotSame(t, parentLeftCertificate, leftCertificate)
	assert.NotSame(t, &parentRight[0], &rightValues[0])
	assert.NotSame(t, parentRightCertificate, rightCertificate)
	parentLeftAgain, parentLeftCertificateAgain, err := parent.certifiedPublishedValues("left")
	require.NoError(t, err)
	parentRightAgain, parentRightCertificateAgain, err := parent.certifiedPublishedValues("right")
	require.NoError(t, err)
	assert.Same(t, &parentLeft[0], &parentLeftAgain[0])
	assert.Same(t, parentLeftCertificate, parentLeftCertificateAgain)
	assert.Same(t, &parentRight[0], &parentRightAgain[0])
	assert.Same(t, parentRightCertificate, parentRightCertificateAgain)
}

func TestIncrementalGroupMemoRankedGenerationRetention(t *testing.T) {
	instance := incrementalInstanceResult{
		component: "producer", source: "routes", namespace: "default", name: "route",
		result: rankedFragmentResult(t,
			incrementalRankedFragment{"stable", "one", "100", "one"},
			incrementalRankedFragment{"stable", "two", "200", "two"},
			incrementalRankedFragment{"changed", "one", "100", "before"},
		),
	}
	parent, err := newIncrementalGroupIndex().replace(&instance, nil)
	require.NoError(t, err)
	_, err = parent.rankedFragments("stable", "")
	require.NoError(t, err)
	_, err = parent.rankedFragments("changed", "")
	require.NoError(t, err)
	stable := incrementalGroupRankedMemoEntry(t, parent, "stable")
	changed := incrementalGroupRankedMemoEntry(t, parent, "changed")

	instance.result = rankedFragmentResult(t,
		incrementalRankedFragment{"stable", "one", "100", "one"},
		incrementalRankedFragment{"stable", "two", "200", "two"},
		incrementalRankedFragment{"changed", "one", "100", "after"},
	)
	child, err := parent.replace(&instance, nil)
	require.NoError(t, err)
	assert.Same(t, stable, incrementalGroupRankedMemoEntry(t, child, "stable"))
	assert.Nil(t, incrementalGroupRankedMemoEntryIfPresent(child, "changed"))
	assert.Same(t, changed, incrementalGroupRankedMemoEntry(t, parent, "changed"))
	text, err := child.rankedFragments("changed", "")
	require.NoError(t, err)
	assert.Equal(t, "after", text)
	assert.NotSame(t, changed, incrementalGroupRankedMemoEntry(t, child, "changed"))
}

func TestIncrementalGroupMemoMultiHopWinnerRestoration(t *testing.T) {
	winner := incrementalInstanceResult{
		component: "producer", source: "routes", namespace: "default", name: "a",
		result: publishedResult(t, memoPublicationValue(t, "values", "shared", "winner")),
	}
	fallback := incrementalInstanceResult{
		component: "producer", source: "routes", namespace: "default", name: "z",
		result: publishedResult(t, memoPublicationValue(t, "values", "shared", "fallback")),
	}
	index := newIncrementalGroupIndex()
	var err error
	index, err = index.replace(&fallback, nil)
	require.NoError(t, err)
	index, err = index.replace(&winner, nil)
	require.NoError(t, err)
	values, certificate, err := index.certifiedPublishedValues("values")
	require.NoError(t, err)
	projection, exists := index.publicationWinnersByLocation.Root().Get(incrementalOrderedTuple("values"))
	require.True(t, exists)

	restored, err := index.replace(&winner, nil)
	require.NoError(t, err)
	restoredProjection, exists := restored.publicationWinnersByLocation.Root().Get(incrementalOrderedTuple("values"))
	require.True(t, exists)
	assert.Same(t, projection.Root(), restoredProjection.Root())
	restoredValues, restoredCertificate, err := restored.certifiedPublishedValues("values")
	require.NoError(t, err)
	assert.Same(t, &values[0], &restoredValues[0])
	assert.Same(t, certificate, restoredCertificate)

	winner.result = publishedResult(t, memoPublicationValue(t, "values", "shared", "changed"))
	changed, err := restored.replace(&winner, nil)
	require.NoError(t, err)
	assert.Nil(t, incrementalGroupPublishedMemoEntryIfPresent(changed, "values"))
	changedValues, changedCertificate, err := changed.certifiedPublishedValues("values")
	require.NoError(t, err)
	require.Equal(t, []any{"changed"}, changedValues)
	assert.NotSame(t, &values[0], &changedValues[0])
	assert.NotSame(t, certificate, changedCertificate)
}

func TestIncrementalGroupMemoBoundsLatestGenerationToCurrentCells(t *testing.T) {
	const stableCells = 32
	const dynamicCells = 32
	instance := incrementalInstanceResult{
		component: "producer", source: "routes", namespace: "default", name: "route",
		result: memoChurnResult(t, 0, stableCells, dynamicCells),
	}
	index, err := newIncrementalGroupIndex().replace(&instance, nil)
	require.NoError(t, err)
	for cellIndex := range stableCells {
		cell := fmt.Sprintf("stable-%02d", cellIndex)
		_, _, err = index.certifiedPublishedValues(cell)
		require.NoError(t, err)
		_, err = index.rankedFragments(cell, "")
		require.NoError(t, err)
	}
	for cellIndex := range dynamicCells {
		cell := fmt.Sprintf("dynamic-00-%02d", cellIndex)
		_, _, err = index.certifiedPublishedValues(cell)
		require.NoError(t, err)
		_, err = index.rankedFragments(cell, "")
		require.NoError(t, err)
	}
	stablePublished := incrementalGroupPublishedMemoEntry(t, index, "stable-00")
	stableRanked := incrementalGroupRankedMemoEntry(t, index, "stable-00")
	require.Equal(t, stableCells+dynamicCells, incrementalGroupMemoEntryCount(index.memo, false))
	require.Equal(t, stableCells+dynamicCells, incrementalGroupMemoEntryCount(index.memo, true))

	for generation := 1; generation <= 20; generation++ {
		parent := index
		instance.result = memoChurnResult(t, generation, stableCells, dynamicCells)
		index, err = parent.replace(&instance, nil)
		require.NoError(t, err)
		assert.Equal(t, stableCells+dynamicCells, incrementalGroupMemoEntryCount(parent.memo, false))
		assert.Equal(t, stableCells, incrementalGroupMemoEntryCount(index.memo, false))
		assert.Equal(t, stableCells, incrementalGroupMemoEntryCount(index.memo, true))
		assert.Same(t, stablePublished, incrementalGroupPublishedMemoEntry(t, index, "stable-00"))
		assert.Same(t, stableRanked, incrementalGroupRankedMemoEntry(t, index, "stable-00"))
		for cellIndex := range dynamicCells {
			cell := fmt.Sprintf("dynamic-%02d-%02d", generation, cellIndex)
			_, _, err = index.certifiedPublishedValues(cell)
			require.NoError(t, err)
			_, err = index.rankedFragments(cell, "")
			require.NoError(t, err)
		}
		assert.Equal(t, stableCells+dynamicCells, incrementalGroupMemoEntryCount(index.memo, false))
		assert.Equal(t, stableCells+dynamicCells, incrementalGroupMemoEntryCount(index.memo, true))
	}
	removed, err := index.remove(instance.component, instance.source, instance.namespace, instance.name)
	require.NoError(t, err)
	assert.Zero(t, incrementalGroupMemoEntryCount(removed.memo, false))
	assert.Zero(t, incrementalGroupMemoEntryCount(removed.memo, true))
}

func TestIncrementalGroupMemoDetachedMutationDoesNotPoisonWarmValues(t *testing.T) {
	instance := incrementalInstanceResult{
		component: "producer", source: "routes", namespace: "default", name: "route",
		result: publishedResult(t, memoPublicationValue(t, "values", "key", map[string]any{
			"nested": map[string]any{"value": "original"},
		})),
	}
	index, err := newIncrementalGroupIndex().replace(&instance, nil)
	require.NoError(t, err)
	warm, certificate, err := index.certifiedPublishedValues("values")
	require.NoError(t, err)
	detached, err := decodeIncrementalPublishedWinners(index, "values")
	require.NoError(t, err)
	detached[0].(map[string]any)["nested"].(map[string]any)["value"] = "poison"

	reused, reusedCertificate, err := index.certifiedPublishedValues("values")
	require.NoError(t, err)
	assert.Equal(t, "original", reused[0].(map[string]any)["nested"].(map[string]any)["value"])
	assert.Same(t, &warm[0], &reused[0])
	assert.Same(t, certificate, reusedCertificate)
}

func TestIncrementalGroupMemoRejectsCrossCellProjectionAlias(t *testing.T) {
	instance := incrementalInstanceResult{
		component: "producer", source: "routes", namespace: "default", name: "route",
		result: publishedResult(t, memoPublicationValue(t, "first", "key", "value")),
	}
	index, err := newIncrementalGroupIndex().replace(&instance, nil)
	require.NoError(t, err)
	_, _, err = index.certifiedPublishedValues("first")
	require.NoError(t, err)
	first, exists := index.publicationWinnersByLocation.Root().Get(incrementalOrderedTuple("first"))
	require.True(t, exists)
	projectionTxn := index.publicationWinnersByLocation.Txn()
	projectionTxn.Insert(incrementalOrderedTuple("second"), first)
	poisoned := *index
	poisoned.publicationWinnersByLocation = projectionTxn.Commit()
	poisoned.authenticate()

	_, _, err = poisoned.certifiedPublishedValues("second")
	require.ErrorContains(t, err, "mismatched cell")
}

func TestIncrementalGroupMemoRejectsSubstitutionAndCorruptEntries(t *testing.T) {
	instance := incrementalInstanceResult{
		component: "producer", source: "routes", namespace: "default", name: "route",
		result: publishedResult(t, memoPublicationValue(t, "values", "key", "value")),
	}
	index, err := newIncrementalGroupIndex().replace(&instance, nil)
	require.NoError(t, err)
	_, _, err = index.certifiedPublishedValues("values")
	require.NoError(t, err)

	other := newIncrementalGroupIndex()
	substituted := *index
	substituted.memo = other.memo
	substituted.authenticate()
	assert.False(t, substituted.rootsAvailable())
	_, _, err = substituted.certifiedPublishedValues("values")
	require.Error(t, err)

	updated, err := index.replace(&instance, nil)
	require.NoError(t, err)
	stale := *updated
	stale.memo = index.memo
	stale.authenticate()
	assert.False(t, stale.rootsAvailable())
	_, _, err = stale.certifiedPublishedValues("values")
	require.Error(t, err)

	index.memo.state.mu.Lock()
	stateCopy := &incrementalGroupMemoState{
		authority:  index.memo.state.authority,
		generation: index.memo.state.generation,
		seal:       index.memo.state,
		published:  index.memo.state.published,
		ranked:     index.memo.state.ranked,
	}
	index.memo.state.mu.Unlock()
	memoCopy := *index.memo
	memoCopy.state = stateCopy
	memoCopy.seal = &memoCopy
	copiedState := *index
	copiedState.memo = &memoCopy
	copiedState.authenticate()
	assert.False(t, copiedState.rootsAvailable())
	_, _, err = copiedState.certifiedPublishedValues("values")
	require.Error(t, err)

	index.memo.state.mu.Lock()
	key := incrementalOrderedTuple("values")
	entry, exists := index.memo.state.published.Root().Get(key)
	require.True(t, exists)
	corrupt := *entry
	corrupt.key.cell = "other"
	corrupt.seal = &corrupt
	txn := index.memo.state.published.Txn()
	txn.Insert(key, &corrupt)
	index.memo.state.published = txn.Commit()
	index.memo.state.mu.Unlock()
	_, _, err = index.certifiedPublishedValues("values")
	require.ErrorContains(t, err, "invalid provenance")
}

func TestIncrementalGroupMemoRejectsNilStateRoots(t *testing.T) {
	for _, ranked := range []bool{false, true} {
		name := "published"
		if ranked {
			name = "ranked"
		}
		t.Run(name, func(t *testing.T) {
			instance := incrementalInstanceResult{
				component: "producer", source: "routes", namespace: "default", name: "route",
				result: rankedFragmentResult(t,
					incrementalRankedFragment{"values", "key", "100", "value"},
				),
			}
			index, err := newIncrementalGroupIndex().replace(&instance, nil)
			require.NoError(t, err)
			index.memo.state.mu.Lock()
			if ranked {
				index.memo.state.ranked = nil
			} else {
				index.memo.state.published = nil
			}
			index.memo.state.mu.Unlock()

			assert.False(t, index.rootsAvailable())
			require.ErrorContains(t, index.validateAuthentication(), "authentication seal")
			_, _, err = index.addPreparedBatch(nil)
			require.ErrorContains(t, err, "authentication seal")
		})
	}
}

func TestIncrementalGroupMemoRejectsEquivalentProjectionRoot(t *testing.T) {
	instance := incrementalInstanceResult{
		component: "producer", source: "routes", namespace: "default", name: "route",
		result: rankedFragmentResult(t,
			incrementalRankedFragment{"values", "key", "100", "value"},
		),
	}
	index, err := newIncrementalGroupIndex().replace(&instance, nil)
	require.NoError(t, err)
	_, _, err = index.certifiedPublishedValues("values")
	require.NoError(t, err)
	_, err = index.rankedFragments("values", "")
	require.NoError(t, err)

	for _, ranked := range []bool{false, true} {
		name := "published"
		projectionRoot := index.publicationWinnersByLocation
		if ranked {
			name = "ranked"
			projectionRoot = index.publicationWinnersByRank
		}
		t.Run(name, func(t *testing.T) {
			cell, exists := projectionRoot.Root().Get(incrementalOrderedTuple("values"))
			require.True(t, exists)
			outer := projectionRoot.Txn()
			outer.Insert(incrementalOrderedTuple("values"), cloneOrderedTree(cell))
			poisoned := *index
			if ranked {
				poisoned.publicationWinnersByRank = outer.Commit()
			} else {
				poisoned.publicationWinnersByLocation = outer.Commit()
			}
			poisoned.authenticate()
			if ranked {
				_, err = poisoned.rankedFragments("values", "")
			} else {
				_, _, err = poisoned.certifiedPublishedValues("values")
			}
			require.ErrorContains(t, err, "invalid provenance")
		})
	}
}

func TestIncrementalGroupMemoRejectsCopiedRankedPayload(t *testing.T) {
	instance := incrementalInstanceResult{
		component: "producer", source: "routes", namespace: "default", name: "route",
		result: rankedFragmentResult(t,
			incrementalRankedFragment{"values", "key", "100", "value"},
		),
	}
	index, err := newIncrementalGroupIndex().replace(&instance, nil)
	require.NoError(t, err)
	_, err = index.rankedFragments("values", "")
	require.NoError(t, err)
	entry := incrementalGroupRankedMemoEntry(t, index, "values")
	payload := *entry.payload
	payload.text = "poison"
	corrupt := *entry
	corrupt.payload = &payload
	corrupt.seal = &corrupt
	index.memo.state.mu.Lock()
	txn := index.memo.state.ranked.Txn()
	txn.Insert(incrementalOrderedTuple("values"), &corrupt)
	index.memo.state.ranked = txn.Commit()
	index.memo.state.mu.Unlock()

	_, err = index.rankedFragments("values", "")
	require.ErrorContains(t, err, "invalid provenance")
}

func TestIncrementalGroupMemoUsesCanonicalEmptyValue(t *testing.T) {
	index := newIncrementalGroupIndex()
	first, firstCertificate, err := index.certifiedPublishedValues("missing-one")
	require.NoError(t, err)
	second, secondCertificate, err := index.certifiedPublishedValues("missing-two")
	require.NoError(t, err)
	assert.Empty(t, first)
	assert.Empty(t, second)
	assert.Zero(t, cap(first))
	assert.Zero(t, cap(second))
	assert.Same(t, firstCertificate, secondCertificate)
	assert.True(t, firstCertificate.Guards(first))
	assert.True(t, secondCertificate.Guards(second))
}

func incrementalGroupMemoEntryCount(memo *incrementalGroupMemo, ranked bool) int {
	if memo == nil || memo.state == nil {
		return -1
	}
	memo.state.mu.Lock()
	defer memo.state.mu.Unlock()
	if ranked {
		if memo.state.ranked == nil {
			return -1
		}
		return memo.state.ranked.Len()
	}
	if memo.state.published == nil {
		return -1
	}
	return memo.state.published.Len()
}

func incrementalGroupPublishedMemoEntry(
	t *testing.T,
	index *incrementalGroupIndex,
	cell string,
) *incrementalPublishedValuesMemo {
	t.Helper()
	entry := incrementalGroupPublishedMemoEntryIfPresent(index, cell)
	require.NotNil(t, entry)
	return entry
}

func incrementalGroupPublishedMemoEntryIfPresent(
	index *incrementalGroupIndex,
	cell string,
) *incrementalPublishedValuesMemo {
	if index == nil || index.memo == nil || index.memo.state == nil {
		return nil
	}
	index.memo.state.mu.Lock()
	defer index.memo.state.mu.Unlock()
	if index.memo.state.published == nil {
		return nil
	}
	entry, _ := index.memo.state.published.Root().Get(incrementalOrderedTuple(cell))
	return entry
}

func incrementalGroupRankedMemoEntry(
	t *testing.T,
	index *incrementalGroupIndex,
	cell string,
) *incrementalRankedFragmentsMemo {
	t.Helper()
	entry := incrementalGroupRankedMemoEntryIfPresent(index, cell)
	require.NotNil(t, entry)
	return entry
}

func incrementalGroupRankedMemoEntryIfPresent(
	index *incrementalGroupIndex,
	cell string,
) *incrementalRankedFragmentsMemo {
	if index == nil || index.memo == nil || index.memo.state == nil {
		return nil
	}
	index.memo.state.mu.Lock()
	defer index.memo.state.mu.Unlock()
	if index.memo.state.ranked == nil {
		return nil
	}
	entry, _ := index.memo.state.ranked.Root().Get(incrementalOrderedTuple(cell))
	return entry
}

func memoChurnResult(
	t *testing.T,
	generation, stableCells, dynamicCells int,
) incrementalComponentResult {
	t.Helper()
	fragments := make([]incrementalRankedFragment, 0, stableCells+dynamicCells)
	for cellIndex := range stableCells {
		cell := fmt.Sprintf("stable-%02d", cellIndex)
		fragments = append(fragments, incrementalRankedFragment{cell, "key", "100", cell})
	}
	for cellIndex := range dynamicCells {
		cell := fmt.Sprintf("dynamic-%02d-%02d", generation, cellIndex)
		fragments = append(fragments, incrementalRankedFragment{cell, "key", "100", cell})
	}
	return rankedFragmentResult(t, fragments...)
}

func memoPublicationValue(t *testing.T, cell, key string, value any) incrementalPublishedValue {
	t.Helper()
	return incrementalPublishedValue{Cell: cell, Key: key, Value: encodedResourceValue(t, value)}
}

func BenchmarkIncrementalGroupMemoWarmProjection(b *testing.B) {
	for _, size := range []int{300, 1000, 3000} {
		b.Run(fmt.Sprintf("values-%d", size), func(b *testing.B) {
			benchmarkIncrementalGroupMemoWarmValues(b, size)
		})
		b.Run(fmt.Sprintf("ranked-%d", size), func(b *testing.B) {
			benchmarkIncrementalGroupMemoWarmRanked(b, size)
		})
	}
}

func benchmarkIncrementalGroupMemoWarmValues(b *testing.B, size int) {
	b.Helper()
	_, index := incrementalPublicationProjectionBenchmarkFixture(b, size)
	values, certificate, err := index.certifiedPublishedValues("fragments")
	if err != nil || len(values) != size {
		b.Fatalf("prime %d values: %v", len(values), err)
	}
	b.ReportAllocs()
	b.ReportMetric(float64(size), "values")
	b.ResetTimer()
	for range b.N {
		cached, cachedCertificate, readErr := index.certifiedPublishedValues("fragments")
		if readErr != nil || len(cached) != size || cachedCertificate != certificate ||
			&cached[0] != &values[0] {
			b.Fatalf("warm read %d values: %v", len(cached), readErr)
		}
		incrementalPublishedValuesSink = cached
	}
}

func benchmarkIncrementalGroupMemoWarmRanked(b *testing.B, size int) {
	b.Helper()
	_, index := incrementalPublicationProjectionBenchmarkFixture(b, size)
	text, err := index.rankedFragments("fragments", "")
	if err != nil || text == "" {
		b.Fatalf("prime ranked fragments: %v", err)
	}
	b.ReportAllocs()
	b.ReportMetric(float64(size), "fragments")
	b.ResetTimer()
	for range b.N {
		cached, readErr := index.rankedFragments("fragments", "")
		if readErr != nil || cached != text {
			b.Fatalf("warm ranked fragments: %v", readErr)
		}
		incrementalRankedFragmentsMemoSink = cached
	}
}

var incrementalRankedFragmentsMemoSink string
