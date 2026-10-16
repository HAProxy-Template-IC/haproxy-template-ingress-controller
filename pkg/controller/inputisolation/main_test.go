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

package inputisolation

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/haproxy-haptic/HAPTIC/pkg/dataplane/dataplanetest"
)

type haproxyVerdict func(config string) ([]byte, error)

var (
	haproxyChecks atomic.Int64
	haproxyJudge  atomic.Pointer[haproxyVerdict]
)

func TestMain(m *testing.M) {
	restore := dataplanetest.InstallFakeHAProxy(dataplanetest.WithCheck(func(workDir string, args []string) ([]byte, error) {
		haproxyChecks.Add(1)
		judge := haproxyJudge.Load()
		if judge == nil {
			return nil, nil
		}
		config, err := os.ReadFile(filepath.Join(workDir, args[len(args)-1]))
		if err != nil {
			return nil, err
		}
		return (*judge)(string(config))
	}))
	code := m.Run()
	restore()
	os.Exit(code)
}
