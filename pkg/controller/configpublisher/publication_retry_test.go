// Copyright 2025 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package configpublisher

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/auxiliaryfiles"
)

func TestPublishGenerationSupersedesValidationButPreservesDeployOrder(t *testing.T) {
	c := &Component{publicationTerm: 1}
	validationA := &publishWorkItem{}
	validationA.generation, validationA.term, validationA.superseded = c.assignPublishAuthority(false)
	assert.True(t, c.publishWorkCurrent(validationA))

	validationB := &publishWorkItem{}
	validationB.generation, validationB.term, validationB.superseded = c.assignPublishAuthority(false)
	deployed := &publishWorkItem{deployDriven: true}
	deployed.generation, deployed.term, deployed.superseded = c.assignPublishAuthority(true)

	assert.False(t, c.publishWorkCurrent(validationA))
	assert.True(t, c.publishWorkCurrent(deployed))
	assert.True(t, c.publishWorkCurrent(validationB))

	c.mu.Lock()
	c.publicationTerm++
	c.mu.Unlock()
	assert.False(t, c.publishWorkCurrent(deployed))
	assert.False(t, c.publishWorkCurrent(validationB))
}

func TestPublishGenerationInterruptsSupersededRetryWait(t *testing.T) {
	c := &Component{publicationTerm: 1, publishSuperseded: make(chan struct{})}
	work := &publishWorkItem{}
	work.generation, work.term, work.superseded = c.assignPublishAuthority(false)

	waitDone := make(chan bool, 1)
	go func() {
		waitDone <- waitForPublicationRetry(t.Context(), time.Hour, work.superseded)
	}()

	c.assignPublishAuthority(false)
	select {
	case retry := <-waitDone:
		assert.False(t, retry)
	case <-time.After(time.Second):
		t.Fatal("superseded publication remained asleep in retry backoff")
	}

	assert.False(t, c.publishWorkCurrent(work))
}

func TestCloneAuxiliaryFilesOwnsReloadOnPush(t *testing.T) {
	reloadOnPush := false
	original := &dataplane.AuxiliaryFiles{GeneralFiles: []auxiliaryfiles.GeneralFile{{
		Filename: "sidecar-owned", ReloadOnPush: &reloadOnPush,
	}}}

	clone := cloneAuxiliaryFiles(original)
	*original.GeneralFiles[0].ReloadOnPush = true

	assert.False(t, *clone.GeneralFiles[0].ReloadOnPush)
}
