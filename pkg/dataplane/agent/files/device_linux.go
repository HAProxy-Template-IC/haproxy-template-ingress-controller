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

//go:build linux

package files

import (
	"os"

	"golang.org/x/sys/unix"
)

// deviceOf reports the st_dev of dir, which is how the agent tells the mounts
// under its base directory apart.
func deviceOf(dir string) (uint64, error) {
	var st unix.Stat_t
	if err := unix.Stat(dir, &st); err != nil {
		return 0, err
	}
	return st.Dev, nil
}

// mountPointsUnder lists the mount points strictly below root from
// /proc/self/mountinfo.
func mountPointsUnder(root string) ([]string, error) {
	f, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return mountPointsIn(f, root)
}
