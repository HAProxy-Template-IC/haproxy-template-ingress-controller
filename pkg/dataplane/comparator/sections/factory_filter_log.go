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
	"github.com/haproxytech/client-native/v6/models"
)

// filterIdentifier extracts the type identifier from a Filter model.
func filterIdentifier(filter *models.Filter) string { return filter.Type }

// logTargetIdentifier extracts the address identifier from a LogTarget model.
func logTargetIdentifier(logTarget *models.LogTarget) string { return logTarget.Address }

// CRUD builders for filters, log targets and the log-forward top-level section.
var (
	FilterFrontendOps    = NewIndexChildCRUD[*models.Filter]("filter", "filter", "frontend", filterIdentifier)
	FilterBackendOps     = NewIndexChildCRUD[*models.Filter]("filter", "filter", "backend", filterIdentifier)
	LogTargetFrontendOps = NewIndexChildCRUD[*models.LogTarget]("log_target", "log target", "frontend", logTargetIdentifier)
	LogTargetBackendOps  = NewIndexChildCRUD[*models.LogTarget]("log_target", "log target", "backend", logTargetIdentifier)
	LogForwardOps        = NewTopLevelCRUD[*models.LogForward]("log_forward", "log-forward", logForwardName)
)
