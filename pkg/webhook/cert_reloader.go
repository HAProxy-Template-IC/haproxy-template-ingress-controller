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

package webhook

import (
	"crypto/sha256"
	"crypto/tls"
	"fmt"
	"os"
	"sync"
)

// certReloader serves a TLS certificate read from a cert/key file pair,
// reloading it when the file contents change. It satisfies
// tls.Config.GetCertificate so a rotated certificate — e.g. a cert-manager
// renewal written to a mounted Secret — is picked up on the next handshake
// without restarting the server.
//
// Change detection hashes the file contents on every call rather than
// trusting mtime: a renewed cert of identical byte length (two RSA-2048 leaves
// usually are) would otherwise look unchanged. Admission webhooks are
// low-QPS, so reading two small PEM files per handshake is negligible; the
// keypair is only re-parsed when the bytes actually change.
type certReloader struct {
	certFile string
	keyFile  string

	mu     sync.RWMutex
	cached *tls.Certificate
	sum    [sha256.Size]byte
	loaded bool
}

// newCertReloader builds a reloader and eagerly loads the keypair once, so a
// missing or malformed cert surfaces at construction rather than at the first
// handshake.
func newCertReloader(certFile, keyFile string) (*certReloader, error) {
	r := &certReloader{certFile: certFile, keyFile: keyFile}
	if _, err := r.GetCertificate(nil); err != nil {
		return nil, err
	}
	return r, nil
}

// GetCertificate returns the current certificate, reloading from disk first if
// the backing files changed since the last load. On a read/parse error it
// keeps serving the last good certificate (surfacing the error only when no
// certificate has ever loaded), so a torn write mid-rotation never breaks
// admission.
func (r *certReloader) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	certPEM, keyPEM, err := r.read()
	if err != nil {
		return r.lastGoodOr(err)
	}

	h := sha256.New()
	h.Write(certPEM)
	h.Write(keyPEM)
	var sum [sha256.Size]byte
	h.Sum(sum[:0])

	r.mu.RLock()
	if r.loaded && sum == r.sum {
		cached := r.cached
		r.mu.RUnlock()
		return cached, nil
	}
	r.mu.RUnlock()

	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return r.lastGoodOr(fmt.Errorf("parsing webhook keypair (%s, %s): %w", r.certFile, r.keyFile, err))
	}

	r.mu.Lock()
	r.cached = &cert
	r.sum = sum
	r.loaded = true
	r.mu.Unlock()
	return &cert, nil
}

func (r *certReloader) read() (certPEM, keyPEM []byte, err error) {
	certPEM, err = os.ReadFile(r.certFile)
	if err != nil {
		return nil, nil, fmt.Errorf("reading webhook cert %s: %w", r.certFile, err)
	}
	keyPEM, err = os.ReadFile(r.keyFile)
	if err != nil {
		return nil, nil, fmt.Errorf("reading webhook key %s: %w", r.keyFile, err)
	}
	return certPEM, keyPEM, nil
}

func (r *certReloader) lastGoodOr(err error) (*tls.Certificate, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.loaded {
		return r.cached, nil
	}
	return nil, err
}
