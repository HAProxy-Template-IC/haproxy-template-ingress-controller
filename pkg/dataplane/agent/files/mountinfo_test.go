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

package files

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Two bind mounts of one filesystem share st_dev but refuse hardlinks across
// each other, so the probe must read mount points, not devices.
func TestMountPointsInKeepsOnlyMountsBelowRoot(t *testing.T) {
	mountinfo := strings.Join([]string{
		"1 0 0:1 / / rw - overlay overlay rw",
		"2 1 8:2 /@/vol1 /usr/local/etc/haproxy rw - btrfs /dev/sda2 rw",
		"3 2 8:2 /@/vol2 /usr/local/etc/haproxy/general rw - btrfs /dev/sda2 rw",
		"4 1 0:9 / /usr/local/etc/haproxy-other rw - tmpfs tmpfs rw",
		"5 2 0:10 / /usr/local/etc/haproxy/with\\040space rw - tmpfs tmpfs rw",
		"garbage",
	}, "\n")

	points, err := mountPointsIn(strings.NewReader(mountinfo), "/usr/local/etc/haproxy")
	require.NoError(t, err)
	assert.Equal(t, []string{"/usr/local/etc/haproxy/general", "/usr/local/etc/haproxy/with space"}, points)
}
