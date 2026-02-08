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

// Indexed child executors are organized across multiple files by resource type:
//
//   - indexed_child_acl.go:         ACL frontend/backend executors
//   - indexed_child_http_rules.go:  HTTP request/response rule executors
//   - indexed_child_switching.go:   Backend/server switching rule executors
//   - indexed_child_filter_log.go:  Filter and log target executors
//   - indexed_child_tcp.go:         TCP rules, stick rules, HTTP/TCP checks,
//     after-response rules, and capture executors

package executors
