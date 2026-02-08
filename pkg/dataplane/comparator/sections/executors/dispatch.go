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

package executors

import (
	"net/http"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/client"
)

// dispatchAndCheck validates the response from a dispatch call, closing the
// response body and checking the status code. This eliminates the repeated
// 5-line pattern (if err != nil / defer resp.Body.Close() / CheckResponse)
// that appears in every executor function.
func dispatchAndCheck(resp *http.Response, err error, description string) error {
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return client.CheckResponse(resp, description)
}
