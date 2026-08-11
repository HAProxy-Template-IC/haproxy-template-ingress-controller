package controller

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	controllerwebhook "gitlab.com/haproxy-haptic/haptic/pkg/controller/webhook"
	pkgwebhook "gitlab.com/haproxy-haptic/haptic/pkg/webhook"
)

func TestEffectiveResourceAdmissionTimeout(t *testing.T) {
	assert.Equal(t, controllerwebhook.DefaultResourceAdmissionTimeout, effectiveResourceAdmissionTimeout(0))
	assert.Equal(t, 3*time.Second, effectiveResourceAdmissionTimeout(3*time.Second))
}

// A later iteration's server config is deliberately ignored — the listener stays
// bound across reinitializations and only the validator table changes. That is
// correct, but it is also invisible: a caller reading its own ServerConfig would
// believe values the running server does not use.
//
// Every field here comes from a process-level CLI flag and cannot change without
// a restart, so a difference means a wiring bug rather than a reconfiguration.
// It has to be reported, not swallowed.
func TestDescribeServerConfigDiff(t *testing.T) {
	base := &pkgwebhook.ServerConfig{
		Port:         9443,
		Path:         "/validate",
		CertDir:      "/etc/webhook/certs",
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 31 * time.Second,
	}

	tests := []struct {
		name     string
		incoming *pkgwebhook.ServerConfig
		want     string
	}{
		{
			name:     "identical config reports nothing",
			incoming: &pkgwebhook.ServerConfig{Port: 9443, Path: "/validate", CertDir: "/etc/webhook/certs", ReadTimeout: 10 * time.Second, WriteTimeout: 31 * time.Second},
			want:     "",
		},
		{
			name:     "changed port is named",
			incoming: &pkgwebhook.ServerConfig{Port: 9444, Path: "/validate", CertDir: "/etc/webhook/certs", ReadTimeout: 10 * time.Second, WriteTimeout: 31 * time.Second},
			want:     "port 9443->9444",
		},
		{
			name:     "changed write timeout is named",
			incoming: &pkgwebhook.ServerConfig{Port: 9443, Path: "/validate", CertDir: "/etc/webhook/certs", ReadTimeout: 10 * time.Second, WriteTimeout: 20 * time.Second},
			want:     "writeTimeout 31s->20s",
		},
		{
			name:     "several changes are all named",
			incoming: &pkgwebhook.ServerConfig{Port: 9444, Path: "/admit", CertDir: "/etc/webhook/certs", ReadTimeout: 10 * time.Second, WriteTimeout: 31 * time.Second},
			want:     `port 9443->9444, path "/validate"->"/admit"`,
		},
		{
			// Defensive: the first EnsureWebhookServer call has nothing to
			// compare against, and reporting a spurious diff there would train
			// readers to ignore the warning.
			name:     "no bound config yet reports nothing",
			incoming: base,
			want:     "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bound := base
			if tt.name == "no bound config yet reports nothing" {
				bound = nil
			}
			assert.Equal(t, tt.want, describeServerConfigDiff(bound, tt.incoming))
		})
	}
}
