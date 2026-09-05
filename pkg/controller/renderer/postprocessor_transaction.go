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
	"context"
	"errors"
	"fmt"
	"sync"

	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

var errPostProcessPublicationAborted = errors.New("post-process publication was aborted")
var errIrreversiblePublicationAtomicBoundary = errors.New(
	"irreversible publication requires an atomic render input transaction",
)

type postProcessPublicationTransaction struct {
	once         sync.Once
	inner        RenderInputTransaction
	publications stagedRenderPublications
	err          error
}

func newPostProcessPublicationTransaction(
	inner RenderInputTransaction,
	publication templating.PostProcessPublication,
) RenderInputTransaction {
	if publication == nil {
		return inner
	}
	if required, ok := inner.(renderPublicationFinalizerStager); ok {
		if optional, ok := inner.(renderOptionalPublicationStager); ok {
			stagePostProcessPublication(required, optional, publication)
			return inner
		}
	}
	transaction := &postProcessPublicationTransaction{inner: inner}
	stagePostProcessPublication(transaction, transaction, publication)
	return transaction
}

func stagePostProcessPublication(
	required renderPublicationFinalizerStager,
	optional renderOptionalPublicationStager,
	publication templating.PostProcessPublication,
) {
	required.stagePublicationFinalizer(nil, publication.Abort)
	optional.stageOptionalPublication(func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				publication.Abort()
				panic(recovered)
			}
		}()
		publication.Publish()
	})
}

func (t *postProcessPublicationTransaction) HasCandidates() bool {
	return t.inner != nil && t.inner.HasCandidates()
}

func (t *postProcessPublicationTransaction) StagePublication(callback func()) {
	t.stagePublicationFinalizer(callback, nil)
}

func (t *postProcessPublicationTransaction) stagePublicationFinalizer(publish, abort func()) {
	t.publications.stage(publish, abort)
}

func (t *postProcessPublicationTransaction) stageOptionalPublication(publish func()) {
	t.publications.stageOptional(publish)
}

func (t *postProcessPublicationTransaction) bindRenderOutputReservation(
	reservation *renderOutputReservation,
) error {
	return t.publications.bindRenderOutputReservation(reservation)
}

func (t *postProcessPublicationTransaction) Commit(ctx context.Context) error {
	t.once.Do(func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				t.err = errors.Join(
					fmt.Errorf("post-process transaction panicked: %v", recovered),
					t.abortInnerAndPublications(),
				)
			}
		}()
		if cause := context.Cause(ctx); cause != nil {
			t.err = errors.Join(cause, t.abortInnerAndPublications())
			return
		}
		if t.inner != nil {
			t.err = t.publications.prepareTerminalResultRejectingIrreversible()
		} else {
			t.err = t.publications.prepareTerminalResult()
		}
		if t.err != nil {
			t.err = errors.Join(t.err, abortRenderInputTransaction(t.inner))
			return
		}
		if t.inner != nil {
			t.err = t.inner.Commit(ctx)
			if t.err != nil {
				t.err = errors.Join(
					t.err,
					abortRenderInputTransaction(t.inner),
					t.publications.abortResult(),
				)
				return
			}
		}
		t.err = t.publications.completeResult()
	})
	return t.err
}

func (t *postProcessPublicationTransaction) Abort() {
	t.once.Do(func() {
		t.err = errors.Join(errPostProcessPublicationAborted, t.abortInnerAndPublications())
	})
}

func (t *postProcessPublicationTransaction) abortInnerAndPublications() error {
	return errors.Join(
		abortRenderInputTransaction(t.inner),
		t.publications.abortResult(),
	)
}
