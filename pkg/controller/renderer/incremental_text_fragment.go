// Copyright 2026 Philipp Hossner
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package renderer

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

type incrementalStringFragment string

func (f incrementalStringFragment) WriteTo(writer io.Writer) (int64, error) {
	if writer == nil {
		return 0, errors.New("incremental text fragment writer is nil")
	}
	written, err := io.WriteString(writer, string(f))
	if written < 0 || written > len(f) {
		return int64(written), fmt.Errorf("incremental text fragment writer returned invalid count %d", written)
	}
	if err == nil && written != len(f) {
		err = io.ErrShortWrite
	}
	return int64(written), err
}

func materializeIncrementalTextFragment(fragment templating.TextFragment) (string, error) {
	if fragment == nil {
		return "", errors.New("incremental text fragment is nil")
	}
	var output strings.Builder
	reported, err := fragment.WriteTo(&output)
	if reported < 0 || reported != int64(output.Len()) {
		return "", fmt.Errorf(
			"incremental text fragment reported %d for %d bytes",
			reported,
			output.Len(),
		)
	}
	if err != nil {
		return "", err
	}
	return output.String(), nil
}
