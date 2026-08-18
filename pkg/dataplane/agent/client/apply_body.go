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

package client

import (
	"fmt"
	"io"
	"mime/multipart"
	"sync/atomic"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/agent/api"
)

// applySource produces the multipart body of one apply. The caller's part
// readers are single-use, so a retry is only offered while none of them has
// been read.
type applySource struct {
	manifest     *api.Manifest
	manifestJSON []byte
	parts        map[string]io.Reader
	plan         io.Reader
	consumed     atomic.Bool
}

func newApplySource(m *api.Manifest, manifestJSON []byte, parts map[string]io.Reader, plan io.Reader) *applySource {
	return &applySource{manifest: m, manifestJSON: manifestJSON, parts: parts, plan: plan}
}

func (s *applySource) replayable() bool { return !s.consumed.Load() }

// open returns a fresh body reader and its Content-Type. The writer goroutine
// ends when the body is closed, which http.Client does on every outcome.
func (s *applySource) open() (body io.ReadCloser, contentType string) {
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(&countingWriter{w: pw, limit: api.MaxApplyBodyBytes})
	go func() {
		err := s.write(mw)
		if err == nil {
			err = mw.Close()
		}
		_ = pw.CloseWithError(err)
	}()
	return pr, mw.FormDataContentType()
}

func (s *applySource) write(mw *multipart.Writer) error {
	manifest, err := mw.CreateFormField(api.PartManifest)
	if err != nil {
		return err
	}
	if _, err := manifest.Write(s.manifestJSON); err != nil {
		return err
	}
	if s.plan != nil {
		n, err := s.copyPart(mw, api.PartPlan, s.plan, api.MaxPlanBlobBytes)
		if err != nil {
			return err
		}
		if n > api.MaxPlanBlobBytes {
			return fmt.Errorf("agent client: plan blob exceeds %d bytes", api.MaxPlanBlobBytes)
		}
	}
	for _, f := range s.manifest.Files {
		content, ok := s.parts[f.Path]
		if !ok {
			continue
		}
		// A content/manifest size mismatch is a controller bug; catching it
		// here beats letting the agent reject the digest after the bytes
		// crossed the wire.
		n, err := s.copyPart(mw, f.Path, content, f.Size)
		if err != nil {
			return err
		}
		if n != f.Size {
			return fmt.Errorf("agent client: part %q: manifest declares %d bytes, content yielded %d", f.Path, f.Size, n)
		}
	}
	return nil
}

// copyPart streams one part, reading at most limit+1 bytes so the caller can
// tell "exactly limit" from "more than limit".
func (s *applySource) copyPart(mw *multipart.Writer, name string, content io.Reader, limit int64) (int64, error) {
	part, err := mw.CreateFormFile(name, name)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(part, io.LimitReader(&markingReader{r: content, mark: &s.consumed}, limit+1))
	if err != nil {
		return n, fmt.Errorf("agent client: part %q: %w", name, err)
	}
	return n, nil
}

// markingReader records that the single-use source was touched, which retires
// the connect retry for this apply.
type markingReader struct {
	r    io.Reader
	mark *atomic.Bool
}

func (m *markingReader) Read(p []byte) (int, error) {
	n, err := m.r.Read(p)
	if n > 0 {
		m.mark.Store(true)
	}
	return n, err
}

// countingWriter fails the body once it passes the wire limit, so a reader
// that lies about its length cannot hand the agent an oversized request.
type countingWriter struct {
	w       io.Writer
	limit   int64
	written int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	c.written += int64(len(p))
	if c.written > c.limit {
		return 0, fmt.Errorf("agent client: apply body exceeds %d bytes", c.limit)
	}
	return c.w.Write(p)
}
