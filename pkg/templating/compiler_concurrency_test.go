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

package templating

import (
	"context"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestConcurrentEngineBuildsKeepBooleanConstantsIsolated(t *testing.T) {
	for worker := range 16 {
		t.Run(strconv.Itoa(worker), func(t *testing.T) {
			t.Parallel()
			for range 16 {
				engine, err := New(map[string]string{
					"main": `{{ tostring(true) }}|{{ tostring(false) }}`,
				}, nil)
				require.NoError(t, err)
				output, err := engine.Render(context.Background(), "main", nil)
				require.NoError(t, err)
				require.Equal(t, "true|false\n", output)
			}
		})
	}
}
