// Copyright 2026 Philipp Hossner
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

package controller

import (
	"sync"

	"gitlab.com/haproxy-haptic/haptic/pkg/controller/configchange"
)

type iterationReloadAuthority struct {
	mu     sync.RWMutex
	reload *configchange.ReloadRequest
}

func (a *iterationReloadAuthority) Record(reload *configchange.ReloadRequest) {
	if reload == nil || reload.Snapshot == nil {
		return
	}
	a.mu.Lock()
	a.reload = reload
	a.mu.Unlock()
}

func (a *iterationReloadAuthority) Latest() *configchange.ReloadRequest {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.reload
}

func startIterationReloadObserver(setup *componentSetup, authority *iterationReloadAuthority) {
	setup.ErrGroup.Go(func() error {
		select {
		case reload := <-setup.ConfigChangeCh:
			authority.Record(reload)
			setup.Cancel()
		case <-setup.IterCtx.Done():
		}
		return nil
	})
}
