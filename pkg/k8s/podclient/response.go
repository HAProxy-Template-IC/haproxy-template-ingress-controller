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

package podclient

import (
	"context"
	"errors"
	"io"
)

// GetFromPod reads a named pod even when it isn't ready. Zero maxBytes is unlimited.
func (c *Client) GetFromPod(ctx context.Context, name, path string, maxBytes int64) ([]byte, error) {
	if maxBytes < 0 {
		return nil, errors.New("response limit must not be negative")
	}
	ctx, cancel := context.WithTimeout(ctx, loopbackRequestTimeout)
	defer cancel()
	return c.getFromPod(ctx, name, path, maxBytes)
}

func readResponse(reader io.Reader, maxBytes int64) ([]byte, error) {
	if maxBytes == 0 {
		return io.ReadAll(reader)
	}
	body, err := io.ReadAll(io.LimitReader(reader, maxBytes))
	if err != nil {
		return nil, err
	}
	var extra [1]byte
	count, err := io.ReadFull(reader, extra[:])
	if count > 0 {
		return nil, errors.New("pod response exceeds the requested limit")
	}
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	return body, nil
}
