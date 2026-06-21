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

package sections

import (
	"fmt"

	"github.com/haproxytech/client-native/v6/models"
)

// bindIdentifier creates a descriptive identifier for a bind that includes address, port, and SSL info.
func bindIdentifier(bind *models.Bind) string {
	desc := ""
	if bind.Address != "" && bind.Port != nil {
		desc = fmt.Sprintf("%s:%d", bind.Address, *bind.Port)
	} else if bind.Port != nil {
		desc = fmt.Sprintf("*:%d", *bind.Port)
	} else if bind.Name != "" {
		desc = bind.Name
	}

	if bind.Ssl {
		desc += " ssl"
		if bind.SslCertificate != "" {
			desc += fmt.Sprintf(" crt %s", bind.SslCertificate)
		}
	}
	return desc
}

// CRUD builder for binds in a frontend.
var BindFrontendOps = NewNameChildCRUD[*models.Bind](
	"bind", "bind", "frontend",
	func(bind *models.Bind, _ string) string { return bindIdentifier(bind) },
)
