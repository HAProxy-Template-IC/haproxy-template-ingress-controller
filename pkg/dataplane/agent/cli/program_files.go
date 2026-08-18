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

package cli

import (
	"errors"
	"fmt"
	"strings"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/api"
	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
)

func compileMapAdd(op *api.Op, _ Content) (cmds, abort []Command, err error) {
	if err := errors.Join(
		validateToken("map path", op.Path),
		validateToken("map key", op.Key),
		validatePayloadValue("map value", op.Value),
	); err != nil {
		return nil, nil, err
	}
	return []Command{{
		Text:    "add map " + op.Path,
		Payload: op.Key + " " + op.Value + "\n",
	}}, nil, nil
}

// compileMapSet uses the line form, which HAProxy truncates at the first
// space, so the value passes the same check as a command word.
func compileMapSet(op *api.Op, _ Content) (cmds, abort []Command, err error) {
	if err := errors.Join(
		validateToken("map path", op.Path),
		validateToken("map key", op.Key),
		validateToken("map value", op.Value),
	); err != nil {
		return nil, nil, err
	}
	return []Command{{Text: fmt.Sprintf("set map %s %s %s", op.Path, op.Key, op.Value)}}, nil, nil
}

// compileMapDel emits one delete; HAProxy 3.4 removes a single duplicate per
// call, so the executor repeats the command until the key is gone.
func compileMapDel(op *api.Op, _ Content) (cmds, abort []Command, err error) {
	if err := errors.Join(
		validateToken("map path", op.Path),
		validateToken("map key", op.Key),
	); err != nil {
		return nil, nil, err
	}
	return []Command{{Text: fmt.Sprintf("del map %s %s", op.Path, op.Key), Repeat: true}}, nil, nil
}

// compileMapReplace is the versioned atomic switch. `clear map @version` is
// not an abort — it clears on 3.0 and is a no-op on 3.4, so a later commit
// would install what was meant to be discarded — hence a failed chunk leaks
// the version and the executor only counts it.
func compileMapReplace(op *api.Op, content Content) (cmds, abort []Command, err error) {
	if err := validateToken("map path", op.Path); err != nil {
		return nil, nil, err
	}
	body, err := content(op.Path)
	if err != nil {
		return nil, nil, err
	}
	cmds = []Command{{Text: "prepare map " + op.Path, Expect: "version created", Capture: true}}
	chunks, err := chunkLines(mapPayload(body), api.MaxPayloadBytes)
	if err != nil {
		return nil, nil, err
	}
	for _, chunk := range chunks {
		cmds = append(cmds, Command{
			Text:    fmt.Sprintf("add map @%s %s", VersionPlaceholder, op.Path),
			Payload: chunk,
		})
	}
	// `commit map` answers nothing at all on success (verified on 3.0 and
	// 3.4), so any message it does return is a refusal.
	commit := Command{Text: fmt.Sprintf("commit map @%s %s", VersionPlaceholder, op.Path)}
	return append(cmds, commit), nil, nil
}

func compileCert(create bool) compiler {
	return func(op *api.Op, content Content) (cmds, abort []Command, err error) {
		if err := validateToken("certificate path", op.Path); err != nil {
			return nil, nil, err
		}
		pem, err := content(op.Path)
		if err != nil {
			return nil, nil, err
		}
		if create {
			cmds = append(cmds, Command{Text: "new ssl cert " + op.Path, Expect: "New empty certificate store"})
		}
		cmds = append(cmds,
			Command{Text: "set ssl cert " + op.Path, Payload: string(pem), Expect: "transaction"},
			Command{Text: "commit ssl cert " + op.Path, Expect: "Success!"},
		)
		return cmds, []Command{{Text: "abort ssl cert " + op.Path}}, nil
	}
}

func compileCA(create bool) compiler {
	return func(op *api.Op, content Content) (cmds, abort []Command, err error) {
		if err := validateToken("ca-file path", op.Path); err != nil {
			return nil, nil, err
		}
		pem, err := content(op.Path)
		if err != nil {
			return nil, nil, err
		}
		if create {
			cmds = append(cmds, Command{Text: "new ssl ca-file " + op.Path, Expect: "New CA file created"})
		}
		cmds = append(cmds,
			Command{Text: "set ssl ca-file " + op.Path, Payload: string(pem), Expect: "transaction"},
			Command{Text: "commit ssl ca-file " + op.Path, Expect: "Success!"},
		)
		return cmds, []Command{{Text: "abort ssl ca-file " + op.Path}}, nil
	}
}

// compileCRTListAdd uses the payload form: the line form silently discards
// options and SNI filters.
func compileCRTListAdd(op *api.Op, _ Content) (cmds, abort []Command, err error) {
	if err := errors.Join(
		validateToken("crt-list path", op.Path),
		validateToken("certificate", op.Cert),
	); err != nil {
		return nil, nil, err
	}
	entry, err := crtListEntry(op)
	if err != nil {
		return nil, nil, err
	}
	return []Command{{
		Text:    "add ssl crt-list " + op.Path,
		Payload: entry + "\n",
		Expect:  "Success",
	}}, nil, nil
}

func compileCRTListDel(op *api.Op, _ Content) (cmds, abort []Command, err error) {
	if err := errors.Join(
		validateToken("crt-list path", op.Path),
		validateToken("certificate", op.Cert),
	); err != nil {
		return nil, nil, err
	}
	return []Command{{Text: fmt.Sprintf("del ssl crt-list %s %s", op.Path, op.Cert)}}, nil, nil
}

func crtListEntry(op *api.Op) (string, error) {
	var b strings.Builder
	b.WriteString(op.Cert)
	if len(op.Options) > 0 {
		b.WriteString(" [")
		for i, kw := range op.Options {
			if i > 0 {
				b.WriteByte(' ')
			}
			if err := validateToken("crt-list option", kw.Name); err != nil {
				return "", err
			}
			b.WriteString(kw.Name)
			for _, arg := range kw.Args {
				if err := validateToken("crt-list option argument", arg); err != nil {
					return "", err
				}
				b.WriteByte(' ')
				b.WriteString(arg)
			}
		}
		b.WriteByte(']')
	}
	for _, sni := range op.SNIFilters {
		if err := validateToken("sni filter", sni); err != nil {
			return "", err
		}
		b.WriteByte(' ')
		b.WriteString(sni)
	}
	return b.String(), nil
}

// mapPayload renders a map file as the entries the worker is to hold. What
// travels is the plan's own reading of the file: the payload parser has no
// comment syntax, so a '#' header would be stored as a key.
func mapPayload(body []byte) string {
	var b strings.Builder
	for _, entry := range renderplan.ParseMapEntries(string(body)) {
		b.WriteString(entry.Key)
		if entry.Value != "" {
			b.WriteByte(' ')
			b.WriteString(entry.Value)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// chunkLines splits a payload into blocks of at most limit bytes without ever
// cutting a line, because HAProxy applies zero entries of a payload that
// exceeds its buffer.
func chunkLines(body string, limit int) ([]string, error) {
	var chunks []string
	var current strings.Builder
	for _, line := range strings.SplitAfter(body, "\n") {
		if line == "" {
			continue
		}
		if !strings.HasSuffix(line, "\n") {
			line += "\n"
		}
		if len(line) > limit {
			return nil, fmt.Errorf("%w: a line of %d bytes exceeds the %d-byte payload limit",
				ErrUnsafeToken, len(line), limit)
		}
		if current.Len()+len(line) > limit {
			chunks = append(chunks, current.String())
			current.Reset()
		}
		current.WriteString(line)
	}
	if current.Len() > 0 {
		chunks = append(chunks, current.String())
	}
	return chunks, nil
}
