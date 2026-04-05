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

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/comparator/sections/executors"
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

// NewBindFrontendCreate creates an operation to create a bind in a frontend.
func NewBindFrontendCreate(frontendName, bindName string, bind *models.Bind) Operation {
	return NewNameChildOp(
		OperationCreate,
		"bind",
		PriorityBind,
		frontendName,
		bindName,
		bind,
		Identity[*models.Bind],
		executors.BindFrontendCreate(frontendName),
		DescribeNamedChild(OperationCreate, "bind", bindIdentifier(bind), "frontend", frontendName),
	)
}

// NewBindFrontendUpdate creates an operation to update a bind in a frontend.
func NewBindFrontendUpdate(frontendName, bindName string, bind *models.Bind) Operation {
	return NewNameChildOp(
		OperationUpdate,
		"bind",
		PriorityBind,
		frontendName,
		bindName,
		bind,
		Identity[*models.Bind],
		executors.BindFrontendUpdate(frontendName),
		DescribeNamedChild(OperationUpdate, "bind", bindIdentifier(bind), "frontend", frontendName),
	)
}

// NewBindFrontendDelete creates an operation to delete a bind from a frontend.
func NewBindFrontendDelete(frontendName, bindName string, bind *models.Bind) Operation {
	return NewNameChildOp(
		OperationDelete,
		"bind",
		PriorityBind,
		frontendName,
		bindName,
		bind,
		Nil[*models.Bind],
		executors.BindFrontendDelete(frontendName),
		DescribeNamedChild(OperationDelete, "bind", bindIdentifier(bind), "frontend", frontendName),
	)
}
