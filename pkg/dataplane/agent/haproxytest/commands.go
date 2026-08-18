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

package haproxytest

import (
	"fmt"
	"strings"
)

func (h *HAProxy) dispatch(command, payload string, master bool) reply {
	if command == "" || command == "quit" || command == "experimental-mode on" || command == "prompt" {
		return silent()
	}
	h.mu.Lock()
	h.m.Commands = append(h.m.Commands, command)
	reject := h.m.Reject
	h.mu.Unlock()
	if reject != nil {
		if msg, rejected := reject(command); rejected {
			return failure("%s", msg)
		}
	}
	if master {
		return h.dispatchMaster(command, payload)
	}
	return h.dispatchWorker(command, payload)
}

func (h *HAProxy) dispatchMaster(command, payload string) reply {
	if relayed, found := strings.CutPrefix(command, "@1 "); found {
		return h.dispatchWorker(relayed, payload)
	}
	switch command {
	case "reload":
		return h.reload()
	case "show proc":
		h.mu.Lock()
		defer h.mu.Unlock()
		return dump(fmt.Sprintf("#<PID>          <type>          <reloads>\n%d              worker          0", h.m.Pid))
	}
	return failure("Unknown command '%s'.", command)
}

func (h *HAProxy) reload() reply {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.m.ReloadFails {
		return dump("Success=0\n--\n" + h.m.ReloadLog)
	}
	h.m.Pid++
	if h.m.OnReload != nil {
		h.m.OnReload(&h.m)
	}
	return dump("Success=1\n--\n" + h.m.ReloadLog)
}

// workerVerbs is the model's command table, keyed by first word.
var workerVerbs = map[string]func(*HAProxy, string, string) reply{
	"show":      (*HAProxy).show,
	"add":       (*HAProxy).add,
	"del":       (*HAProxy).del,
	"set":       (*HAProxy).set,
	"new":       (*HAProxy).create,
	"commit":    (*HAProxy).commit,
	"abort":     (*HAProxy).abort,
	"publish":   (*HAProxy).publish,
	"unpublish": (*HAProxy).unpublish,
	"enable":    (*HAProxy).enable,
	"disable":   (*HAProxy).disable,
	"shutdown":  (*HAProxy).shutdownSessions,
	"prepare":   (*HAProxy).prepareMap,
	"wait":      (*HAProxy).wait,
}

func (h *HAProxy) dispatchWorker(command, payload string) reply {
	verb, rest := cut(command)
	handler, ok := workerVerbs[verb]
	if !ok {
		return failure("Unknown command '%s'. Please enter one of the following commands only.", command)
	}
	return handler(h, rest, payload)
}

// The object words the model's dispatchers match on, spelled once.
const (
	objBackend = "backend"
	objServer  = "server"
	objMap     = "map"
	objSSL     = "ssl"
	objCert    = "cert"
	objCAFile  = "ca-file"
	objCRLFile = "crl-file"
	objCRTList = "crt-list"
)

func cut(s string) (head, rest string) {
	head, rest, _ = strings.Cut(strings.TrimSpace(s), " ")
	return head, strings.TrimSpace(rest)
}
