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

package deployplan

import (
	"fmt"
	"slices"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/api"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
)

// stateMaint is the set server state a render's disabled server asks for;
// leaving it again is `enable server`, not the opposite set.
const stateMaint = "maint"

// composer turns render records into ops for one pod. Every method returns a
// reason instead of ops when HAProxy would refuse the command; the caller
// decides whether that reason reloads (Diff) or drops the op (InPlace).
type composer struct {
	caps      Caps
	inventory *api.Inventory
	// created are the runtime-store objects this diff creates before anything
	// can name them; the agent folds them into its inventory the same way.
	created map[string]bool
	// pendingServerDeletes and pendingBackendDeletes are the pod's baseline plus
	// what this diff has composed, because the cap is on the queue, not the ACK.
	pendingServerDeletes  int
	pendingBackendDeletes int
}

// addServer composes add server plus the enable that takes it out of MAINT.
func (c *composer) addServer(be *renderplan.Backend, srv *renderplan.Server) (ops []api.Op, reason string) {
	switch {
	case !c.caps.DynamicServers:
		return nil, "this HAProxy has no add server"
	case !dynamicBalance(be.Balance, be.HashType):
		return nil, fmt.Sprintf("balance %q takes no dynamic server", balanceOf(be))
	case !api.SafeToken(srv.Name):
		return nil, "the name is not a safe runtime token"
	}
	if reason := endpointReason(srv.Address, srv.Port); reason != "" {
		return nil, reason
	}
	keywords := mergeKeywords(be.DefaultServer, srv.Extra)
	if bad := c.ineligibleKeyword(keywords); bad != "" {
		return nil, fmt.Sprintf("keyword %s cannot be set on a dynamic server", bad)
	}
	keywords = c.withRuntimeKeywords(keywords, srv.GUID)
	add := api.Op{
		Kind:     api.OpServerAdd,
		Backend:  be.Name,
		Server:   srv.Name,
		Address:  srv.Address,
		Port:     srv.Port,
		Keywords: keywords,
	}
	if srv.Disabled {
		// A dynamic server starts in MAINT, which is what disabled asks for.
		return []api.Op{add}, ""
	}
	return []api.Op{add, {
		Kind:    api.OpServerEnable,
		Backend: be.Name,
		Server:  srv.Name,
		Health:  hasKeyword(keywords, keywordCheck),
	}}, ""
}

// updateServer composes the value changes HAProxy applies in place. It takes
// the backend because leaving MAINT needs the merged keyword set: `enable
// health` is only accepted on a server that carries `check`.
func (c *composer) updateServer(be *renderplan.Backend, prev, next *renderplan.Server) (ops []api.Op, reason string) {
	if prev.GUID != next.GUID || !slices.EqualFunc(prev.Extra, next.Extra, sameKeyword) {
		return nil, "keywords changed, which set server cannot express"
	}
	backend := be.Name
	ops = make([]api.Op, 0, 3)
	if prev.Address != next.Address || prev.Port != next.Port {
		if reason := endpointReason(next.Address, next.Port); reason != "" {
			return nil, reason
		}
		ops = append(ops, api.Op{
			Kind: api.OpServerSetAddr, Backend: backend, Server: next.Name,
			Address: next.Address, Port: next.Port,
		})
	}
	if !sameWeight(prev.Weight, next.Weight) {
		if next.Weight == nil {
			return nil, "the weight keyword was dropped, which set server cannot express"
		}
		weight := *next.Weight
		ops = append(ops, api.Op{Kind: api.OpServerSetWeight, Backend: backend, Server: next.Name, Weight: &weight})
	}
	switch {
	case prev.Disabled == next.Disabled:
	case next.Disabled:
		ops = append(ops, api.Op{
			Kind: api.OpServerSetState, Backend: backend, Server: next.Name, State: stateMaint,
		})
	default:
		// `set server state ready` leaves a health check that never started
		// disabled, so a dynamic server added in MAINT would take traffic with
		// no check at all; `enable server` clears the same state and takes the
		// health check with it.
		ops = append(ops, api.Op{
			Kind: api.OpServerEnable, Backend: backend, Server: next.Name,
			Health: hasKeyword(mergeKeywords(be.DefaultServer, next.Extra), keywordCheck),
		})
	}
	if len(ops) == 0 {
		return nil, ""
	}
	return ops, ""
}

// removeServer composes the deferred delete: stop traffic, wait for the last
// session, then delete. The agent owns the shutdown-sessions retry.
func (c *composer) removeServer(backend string, srv *renderplan.Server) (ops []api.Op, reason string) {
	switch {
	case !c.caps.DynamicServers:
		return nil, "this HAProxy has no del server"
	case c.pendingServerDeletes >= api.MaxPendingServerDeletes:
		return nil, fmt.Sprintf("%d server deletes already pending", c.pendingServerDeletes)
	case !api.SafeToken(srv.Name):
		return nil, "the name is not a safe runtime token"
	}
	c.pendingServerDeletes++
	return []api.Op{
		{Kind: api.OpServerDisable, Backend: backend, Server: srv.Name},
		{Kind: api.OpServerWaitRemovable, Backend: backend, Server: srv.Name, TimeoutMs: removableTimeoutMs},
		{Kind: api.OpServerDel, Backend: backend, Server: srv.Name},
	}, ""
}

// withRuntimeKeywords adds what the record expresses outside the keyword list:
// the server's GUID and, where HAProxy takes it, an immediately usable server.
func (c *composer) withRuntimeKeywords(keywords []api.KeywordArg, guid string) []api.KeywordArg {
	if guid != "" && !hasKeyword(keywords, keywordGUID) {
		keywords = append(keywords, api.KeywordArg{Name: keywordGUID, Args: []string{guid}})
	}
	if c.caps.ServerInitState && hasKeyword(keywords, keywordCheck) && !hasKeyword(keywords, keywordInitState) {
		keywords = append(keywords, api.KeywordArg{Name: keywordInitState, Args: []string{"up"}})
	}
	return keywords
}

func sameWeight(prev, next *int) bool {
	if prev == nil || next == nil {
		return prev == next
	}
	return *prev == *next
}

func sameKeyword(prev, next renderplan.KeywordArg) bool {
	return prev.Name == next.Name && slices.Equal(prev.Args, next.Args)
}
