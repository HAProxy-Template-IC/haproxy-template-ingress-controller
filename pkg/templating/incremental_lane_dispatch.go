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

package templating

import (
	"strconv"
	"strings"
)

const incrementalLaneDispatchTargets = 256

func incrementalLaneDispatchName(start, end int) string {
	return incrementalVectorIdentifierPrefix + "dispatch_" + strconv.Itoa(start) + "_" + strconv.Itoa(end)
}

// Balanced branches bound Scriggo's live condition registers as entrypoints grow.
func writeIncrementalLaneDispatch(source *strings.Builder, entryPoints []string, laneExpression string, start, end int) {
	if start >= end {
		return
	}
	if end-start == 1 {
		source.WriteString("{% if " + laneExpression + " == " + strconv.Itoa(start) + " %}{{ render " +
			strconv.Quote(entryPoints[start]) + " }}{% end %}")
		return
	}
	middle := start + (end-start)/2
	source.WriteString("{% if " + laneExpression + " < " + strconv.Itoa(middle) + " %}")
	writeIncrementalLaneDispatchBranch(source, entryPoints, laneExpression, start, middle, end-start)
	source.WriteString("{% else %}")
	writeIncrementalLaneDispatchBranch(source, entryPoints, laneExpression, middle, end, end-start)
	source.WriteString("{% end %}")
}

func writeIncrementalLaneDispatchBranch(source *strings.Builder, entryPoints []string, laneExpression string, start, end, parentSize int) {
	if parentSize > incrementalLaneDispatchTargets {
		macroName := incrementalLaneDispatchName(start, end)
		laneName := incrementalVectorIdentifierPrefix + "dispatch_lane"
		// Instantiate dispatch closures within the active vector generation.
		source.WriteString("{% macro " + macroName + "(" + laneName + " int) %}")
		writeIncrementalLaneDispatch(source, entryPoints, laneName, start, end)
		source.WriteString("{% end %}{{ " + macroName + "(" + laneExpression + ") }}")
		return
	}
	writeIncrementalLaneDispatch(source, entryPoints, laneExpression, start, end)
}
