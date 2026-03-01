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

package templating

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// haproxyGUIDMaxLen is the maximum length of a HAProxy GUID (127 characters).
const haproxyGUIDMaxLen = 127

// guidHashLen is the length of the truncation hash suffix (8 hex chars = 32 bits).
const guidHashLen = 8

// scriggoMakeGUID builds a HAProxy GUID from parts joined by ":".
// If the result exceeds 127 characters, the name is truncated and a short hash
// suffix is appended to preserve uniqueness.
//
// Usage in Scriggo templates:
//
//	guid {{ make_guid("be", backendKey) }}
//	guid {{ make_guid("srv", bkName, "SRV_" + tostring(i)) }}
func scriggoMakeGUID(parts ...interface{}) string {
	strs := make([]string, len(parts))
	for i, p := range parts {
		strs[i] = fmt.Sprint(p)
	}

	guid := strings.Join(strs, ":")
	if len(guid) <= haproxyGUIDMaxLen {
		return guid
	}

	return truncateGUID(guid)
}

// truncateGUID shortens a GUID that exceeds haproxyGUIDMaxLen by replacing the
// tail with a hash. The format is: "<truncated>.<hash8>" where hash8 is the
// first 8 hex characters of the SHA-256 of the full GUID.
// The "." separator is used because HAProxy GUIDs only allow alphanumeric, ".", ":", "-", "_".
func truncateGUID(guid string) string {
	hash := sha256.Sum256([]byte(guid))
	hashHex := hex.EncodeToString(hash[:])[:guidHashLen]

	// Budget: haproxyGUIDMaxLen - len(".") - guidHashLen
	prefixLen := haproxyGUIDMaxLen - 1 - guidHashLen
	return guid[:prefixLen] + "." + hashHex
}
