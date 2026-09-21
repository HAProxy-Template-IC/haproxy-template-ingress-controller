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

package templating

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestScriggoPublicKeyInfo(t *testing.T) {
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	edKey, _, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	for _, tc := range []struct {
		name string
		key  any
		want map[string]any
	}{
		{"RSA", &rsaKey.PublicKey, map[string]any{"algorithm": "RSA", "bits": 2048, "curve": ""}},
		{"ECDSA", &ecKey.PublicKey, map[string]any{"algorithm": "ECDSA", "bits": 256, "curve": "P-256"}},
		{"Ed25519", edKey, map[string]any{"algorithm": "Ed25519", "bits": 256, "curve": "Ed25519"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			der, marshalErr := x509.MarshalPKIXPublicKey(tc.key)
			require.NoError(t, marshalErr)
			encoded := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
			info, parseErr := scriggoPublicKeyInfo(" \n" + encoded + "\n")
			require.NoError(t, parseErr)
			tc.want["pem"] = encoded
			require.Equal(t, tc.want, info)
		})
	}
	encoded := string(pem.EncodeToMemory(&pem.Block{Type: "RSA PUBLIC KEY", Bytes: x509.MarshalPKCS1PublicKey(&rsaKey.PublicKey)}))
	info, err := scriggoPublicKeyInfo(encoded)
	require.NoError(t, err)
	require.Equal(t, "RSA", info["algorithm"])
	canonical, rest := pem.Decode([]byte(info["pem"].(string)))
	require.NotNil(t, canonical)
	require.Empty(t, rest)
	require.Equal(t, "PUBLIC KEY", canonical.Type)
	publicKey, err := x509.ParsePKIXPublicKey(canonical.Bytes)
	require.NoError(t, err)
	require.Equal(t, &rsaKey.PublicKey, publicKey)

	private := string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(rsaKey)}))
	for _, tc := range []struct{ name, value string }{
		{"empty", ""}, {"garbage", "not-a-key"}, {"private key", private},
		{"two keys", encoded + encoded}, {"prefix", "garbage\n" + encoded},
		{"suffix", encoded + "garbage"}, {"invalid DER", "-----BEGIN PUBLIC KEY-----\nYWJj\n-----END PUBLIC KEY-----"},
		{"malformed prefix block", "-----BEGIN PUBLIC KEY-----\ninvalid\n" + encoded},
		{"headers", "-----BEGIN PUBLIC KEY-----\nProc-Type: 4,ENCRYPTED\n\nYWJj\n-----END PUBLIC KEY-----"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, parseErr := scriggoPublicKeyInfo(tc.value)
			require.Error(t, parseErr)
			require.Nil(t, got)
		})
	}
}

func TestPublicKeyInfoTemplateCanHandleInvalidInput(t *testing.T) {
	engine, err := New(map[string]string{"test": `{% var key, err = public_key_info("invalid") %}{% if err != nil %}invalid{% else %}{{ key["algorithm"] }}{% end %}`}, nil)
	require.NoError(t, err)
	output, err := engine.Render(context.Background(), "test", nil)
	require.NoError(t, err)
	require.Equal(t, "invalid\n", output)
}

func TestPublicKeyInfoIncrementalEntryPoint(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	require.NoError(t, err)
	encoded := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
	engine, err := New(map[string]string{"test": `{% var info, err = public_key_info(item["key"].(string)) %}{% if err != nil %}invalid{% else %}{{ info["algorithm"] }}:{{ info["bits"] }}{% end %}`}, &Options{
		EntryPoints:            []string{"test"},
		IncrementalEntryPoints: []string{"test"},
	})
	require.NoError(t, err)
	compiled := engine.compiledTemplates["test"]
	require.True(t, compiled.BatchSafe())
	require.NoError(t, compiled.DeterministicSafe())
	replayEngine, err := New(map[string]string{"test": `{% var info, err = public_key_info("invalid") %}{% if err != nil %}invalid{% else %}{{ info["algorithm"] }}{% end %}`}, &Options{EntryPoints: []string{"test"}})
	require.NoError(t, err)
	_, err = replayEngine.PrepareExactCycleReplay([]string{"test"})
	require.NoError(t, err)
	for _, tc := range []struct{ input, want string }{
		{encoded, "ECDSA:256"}, {"invalid", "invalid"}, {encoded, "ECDSA:256"},
	} {
		output, renderErr := engine.RenderIncrementalComponent(t.Context(), "test", incrementalComponentContext(map[string]any{"item": map[string]any{"key": tc.input}}))
		require.NoError(t, renderErr)
		require.Equal(t, tc.want, output)
	}
}
