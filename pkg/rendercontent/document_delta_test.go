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

package rendercontent

import (
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDocumentTransactionAppliesAuthenticatedRankEdits(t *testing.T) {
	base := documentFromChildTexts(t, "a", "b", "c", "d")
	replaced := requireDocumentLeafHandle(t, base, 1)
	deleted := requireDocumentLeafHandle(t, base, 2)
	inserted := requireDocumentGapHandle(t, base, 4)

	transaction, err := base.BeginTransaction()
	require.NoError(t, err)
	require.NoError(t, transaction.ReplaceText(replaced, "B"))
	require.NoError(t, transaction.Delete(deleted))
	require.NoError(t, transaction.InsertText(inserted, "e"))

	next, delta, err := transaction.Commit()
	require.NoError(t, err)
	assertDocumentText(t, next, "aBde")
	require.NoError(t, next.ValidateAuthentication())
	require.NoError(t, delta.ValidateAuthentication())
	structural, err := delta.RequiresFullValidation()
	require.NoError(t, err)
	assert.True(t, structural)
	changes, err := delta.Changes()
	require.NoError(t, err)
	require.Len(t, changes, 3)
	assert.Equal(t, 1, changes[0].Index)
	assertDocumentText(t, changes[0].Before, "b")
	assertDocumentText(t, changes[0].After, "B")
	assert.Equal(t, 2, changes[1].Index)
	assertDocumentText(t, changes[1].Before, "c")
	require.Error(t, changes[1].After.ValidateAuthentication())
	assert.Equal(t, 4, changes[2].Index)
	require.Error(t, changes[2].Before.ValidateAuthentication())
	assertDocumentText(t, changes[2].After, "e")

	applied, err := delta.Apply(base)
	require.NoError(t, err)
	same, err := applied.SameRoot(next)
	require.NoError(t, err)
	assert.True(t, same)

	again, againDelta, err := transaction.Commit()
	require.NoError(t, err)
	assert.Equal(t, delta, againDelta)
	same, err = again.SameRoot(next)
	require.NoError(t, err)
	assert.True(t, same)
}

func TestDocumentTransactionReusesExactRootForNoOp(t *testing.T) {
	base := documentFromChildTexts(t, "a", "b", "c")
	handle := requireDocumentLeafHandle(t, base, 1)
	transaction, err := base.BeginTransaction()
	require.NoError(t, err)
	require.NoError(t, transaction.ReplaceDocument(handle, documentFromChildTexts(t, "b")))

	next, delta, err := transaction.Commit()
	require.NoError(t, err)
	same, err := base.SameRoot(next)
	require.NoError(t, err)
	assert.False(t, same, "an equal but separately authenticated child is not the same retained leaf")
	assertDocumentText(t, next, "abc")
	unchanged, err := delta.SameRoot()
	require.NoError(t, err)
	assert.False(t, unchanged)

	exactChild := documentFromChildTexts(t, "x")
	var builder DocumentBuilder
	require.NoError(t, builder.AppendDocument(exactChild))
	exactBase, err := builder.Build(nil)
	require.NoError(t, err)
	exactHandle := requireDocumentLeafHandle(t, exactBase, 0)
	exactTransaction, err := exactBase.BeginTransaction()
	require.NoError(t, err)
	require.NoError(t, exactTransaction.ReplaceDocument(exactHandle, exactChild))
	exactNext, exactDelta, err := exactTransaction.Commit()
	require.NoError(t, err)
	same, err = exactBase.SameRoot(exactNext)
	require.NoError(t, err)
	assert.True(t, same)
	unchanged, err = exactDelta.SameRoot()
	require.NoError(t, err)
	assert.True(t, unchanged)
	changes, err := exactDelta.Changes()
	require.NoError(t, err)
	assert.Empty(t, changes)
}

func TestDocumentTransactionRejectsForeignCopiedAndStaleProofs(t *testing.T) {
	base := documentFromChildTexts(t, "a", "b")
	equalBytes := documentFromChildTexts(t, "a", "b")
	handle := requireDocumentLeafHandle(t, base, 0)
	gap := requireDocumentGapHandle(t, base, 1)

	foreignTransaction, err := equalBytes.BeginTransaction()
	require.NoError(t, err)
	require.ErrorIs(t, foreignTransaction.ReplaceText(handle, "x"), errInvalidDocumentLeafHandle)
	_, _, err = foreignTransaction.Commit()
	require.ErrorIs(t, err, errInvalidDocumentLeafHandle)

	copiedHandle := *handle
	transaction, err := base.BeginTransaction()
	require.NoError(t, err)
	require.ErrorIs(t, transaction.ReplaceText(&copiedHandle, "x"), errInvalidDocumentLeafHandle)

	copiedGap := *gap
	transaction, err = base.BeginTransaction()
	require.NoError(t, err)
	require.ErrorIs(t, transaction.InsertText(&copiedGap, "x"), errInvalidDocumentGapHandle)

	transaction, err = base.BeginTransaction()
	require.NoError(t, err)
	require.NoError(t, transaction.ReplaceText(handle, "x"))
	next, delta, err := transaction.Commit()
	require.NoError(t, err)
	_, err = delta.Apply(next)
	require.ErrorIs(t, err, errInvalidDocumentDelta)
	_, err = delta.Apply(equalBytes)
	require.ErrorIs(t, err, errInvalidDocumentDelta)

	copiedDelta := *delta
	_, err = copiedDelta.Apply(base)
	require.ErrorIs(t, err, errInvalidDocumentDelta)

	originalIndex := delta.changes[0].index
	delta.changes[0].index++
	require.ErrorIs(t, delta.ValidateAuthentication(), errInvalidDocumentDelta)
	delta.changes[0].index = originalIndex
	require.NoError(t, delta.ValidateAuthentication())

	copiedTransaction := *mustDocumentTransaction(t, base)
	_, _, err = copiedTransaction.Commit()
	require.ErrorIs(t, err, errInvalidDocumentTransaction)
}

func TestDocumentTransactionFailsAtomicallyOnConflictingEdits(t *testing.T) {
	base := documentFromChildTexts(t, "a", "b")
	handle := requireDocumentLeafHandle(t, base, 0)
	transaction, err := base.BeginTransaction()
	require.NoError(t, err)
	require.NoError(t, transaction.ReplaceText(handle, "x"))
	require.ErrorIs(t, transaction.Delete(handle), errDocumentEditConflict)
	_, _, err = transaction.Commit()
	require.ErrorIs(t, err, errDocumentEditConflict)
	assertDocumentText(t, base, "ab")
}

func TestDocumentTransactionSiblingCommitsAreIndependent(t *testing.T) {
	base := documentFromChildTexts(t, "a", "b", "c")
	handles := []*DocumentLeafHandle{
		requireDocumentLeafHandle(t, base, 0),
		requireDocumentLeafHandle(t, base, 2),
	}
	results := make([]Document, len(handles))
	errors := make([]error, len(handles))
	var group sync.WaitGroup
	for index := range handles {
		group.Add(1)
		go func() {
			defer group.Done()
			transaction, err := base.BeginTransaction()
			if err == nil {
				err = transaction.ReplaceText(handles[index], fmt.Sprintf("%d", index))
			}
			if err == nil {
				results[index], _, err = transaction.Commit()
			}
			errors[index] = err
		}()
	}
	group.Wait()
	for _, err := range errors {
		require.NoError(t, err)
	}
	assertDocumentText(t, results[0], "0bc")
	assertDocumentText(t, results[1], "ab1")
	assertDocumentText(t, base, "abc")
}

func BenchmarkDocumentTransactionReplaceOneOf3000(b *testing.B) {
	values := make([]string, 3000)
	for index := range values {
		values[index] = fmt.Sprintf("route-%06d\n", index)
	}
	base := documentFromChildTexts(b, values...)
	handle := requireDocumentLeafHandle(b, base, len(values)/2)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		transaction, err := base.BeginTransaction()
		if err != nil {
			b.Fatal(err)
		}
		if err := transaction.ReplaceText(handle, "changed\n"); err != nil {
			b.Fatal(err)
		}
		documentSink, _, err = transaction.Commit()
		if err != nil {
			b.Fatal(err)
		}
	}
}

func documentFromChildTexts(tb testing.TB, values ...string) Document {
	tb.Helper()
	var builder DocumentBuilder
	for _, value := range values {
		var childBuilder DocumentBuilder
		_, err := childBuilder.WriteString(value)
		require.NoError(tb, err)
		child, err := childBuilder.Build(nil)
		require.NoError(tb, err)
		require.NoError(tb, builder.AppendDocument(child))
	}
	document, err := builder.Build(nil)
	require.NoError(tb, err)
	return document
}

func requireDocumentLeafHandle(
	tb testing.TB,
	document Document,
	index int,
) *DocumentLeafHandle {
	tb.Helper()
	handle, err := document.LeafHandle(index)
	require.NoError(tb, err)
	return handle
}

func requireDocumentGapHandle(
	tb testing.TB,
	document Document,
	index int,
) *DocumentGapHandle {
	tb.Helper()
	handle, err := document.GapHandle(index)
	require.NoError(tb, err)
	return handle
}

func mustDocumentTransaction(tb testing.TB, document Document) *DocumentTransaction {
	tb.Helper()
	transaction, err := document.BeginTransaction()
	require.NoError(tb, err)
	return transaction
}

func assertDocumentText(tb testing.TB, document Document, expected string) {
	tb.Helper()
	text, err := document.String()
	require.NoError(tb, err)
	assert.Equal(tb, expected, text)
}
