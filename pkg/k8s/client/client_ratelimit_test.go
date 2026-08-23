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
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/client-go/rest"
)

func TestApplyRateLimit(t *testing.T) {
	tests := []struct {
		name            string
		cfg             Config
		wantQPS         float32
		wantBurst       int
		wantRateLimiter bool // whether a shared RateLimiter is installed
	}{
		{
			name:            "default (unset QPS) disables client-side throttling",
			cfg:             Config{},
			wantQPS:         -1,
			wantBurst:       0,
			wantRateLimiter: false,
		},
		{
			name:            "explicit negative QPS disables client-side throttling",
			cfg:             Config{QPS: -1, Burst: 999},
			wantQPS:         -1,
			wantBurst:       0, // burst untouched when throttling is disabled
			wantRateLimiter: false,
		},
		{
			name:            "positive QPS installs a shared limiter with the given burst",
			cfg:             Config{QPS: 100, Burst: 200},
			wantQPS:         100,
			wantBurst:       200,
			wantRateLimiter: true,
		},
		{
			name:            "positive QPS with zero burst defaults burst to 2*QPS",
			cfg:             Config{QPS: 100},
			wantQPS:         100,
			wantBurst:       200,
			wantRateLimiter: true,
		},
		{
			// 2*0.5 truncates to 0; the floor keeps the bucket usable
			// (a zero burst rejects every request and wedges the controller).
			name:            "fractional QPS below 1 floors burst to 1",
			cfg:             Config{QPS: 0.5},
			wantQPS:         0.5,
			wantBurst:       1,
			wantRateLimiter: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rc := &rest.Config{}
			applyRateLimit(rc, tt.cfg)

			assert.Equal(t, tt.wantQPS, rc.QPS, "QPS")
			assert.Equal(t, tt.wantBurst, rc.Burst, "Burst")
			if tt.wantRateLimiter {
				assert.NotNil(t, rc.RateLimiter,
					"a positive QPS must install a shared RateLimiter so every derived clientset draws from one budget")
			} else {
				assert.Nil(t, rc.RateLimiter,
					"disabling client-side throttling must leave RateLimiter nil so no token bucket is created")
			}
		})
	}
}
