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

// Package eventimmutability provides a linter that detects modifications to event struct fields.
//
// This analyzer enforces the immutability contract for event structs in HAPTIC
// controller. Events are passed by reference through the event bus, and consumers must treat
// them as read-only to prevent unintended side effects.
//
// The analyzer detects direct field assignments to event struct types and reports them as violations.
package eventimmutability

import (
	"fmt"
	"go/ast"
	"go/types"
	"os"
	"sort"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
)

const Doc = `detect modifications to event struct fields

Event structs in pkg/controller/events are designed to be immutable after creation.
They are passed by reference through the event bus, and consumers must treat them
as read-only to prevent unintended side effects on other subscribers.

This analyzer detects assignments to fields of event struct types and reports violations.

Example of violation:

	func handleEvent(event *ReconciliationTriggeredEvent) {
		event.Reason = "modified"  // violation: event field mutation detected
	}

Correct usage:

	func handleEvent(event *ReconciliationTriggeredEvent) {
		reason := event.Reason  // OK: read-only access
		log.Info("reason", reason)
	}
`

// Analyzer is the event immutability analyzer.
var Analyzer = &analysis.Analyzer{
	Name:     "eventimmutability",
	Doc:      Doc,
	Requires: []*analysis.Analyzer{inspect.Analyzer},
	Run:      run,
}

func run(pass *analysis.Pass) (interface{}, error) {
	inspect := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)

	// Track the current receiver type and parameters
	var currentRecvType types.Type
	currentParams := make(map[*types.Var]bool)

	// Track discovered event types and analysis statistics
	eventTypes := make(map[string]bool)
	filesAnalyzed := make(map[string]bool)
	eventParametersChecked := 0
	violationsFound := 0

	// Use WithStack to get parent information
	nodeFilter := []ast.Node{
		(*ast.FuncDecl)(nil),
		(*ast.AssignStmt)(nil),
	}

	inspect.WithStack(nodeFilter, func(n ast.Node, push bool, stack []ast.Node) bool {
		if !push {
			return true
		}

		switch node := n.(type) {
		case *ast.FuncDecl:
			// Reset receiver type and parameters for new function
			currentRecvType = nil
			currentParams = make(map[*types.Var]bool)

			// Get receiver type if this is a method
			if node.Recv != nil && len(node.Recv.List) > 0 {
				recvType := pass.TypesInfo.TypeOf(node.Recv.List[0].Type)
				if recvType != nil {
					// Handle pointer receivers
					if ptr, ok := recvType.(*types.Pointer); ok {
						currentRecvType = ptr.Elem()
					} else {
						currentRecvType = recvType
					}
				}
			}

			// Track all function parameters
			if node.Type != nil && node.Type.Params != nil {
				for _, field := range node.Type.Params.List {
					for _, name := range field.Names {
						if obj := pass.TypesInfo.ObjectOf(name); obj != nil {
							if varObj, ok := obj.(*types.Var); ok {
								currentParams[varObj] = true

								// Track if this is an event type parameter
								paramType := varObj.Type()
								if ptr, ok := paramType.(*types.Pointer); ok {
									paramType = ptr.Elem()
								}
								if named, ok := paramType.(*types.Named); ok {
									if obj := named.Obj(); obj != nil && obj.Pkg() != nil {
										if isEventPackage(obj.Pkg().Path()) {
											eventTypes[obj.Name()] = true
											eventParametersChecked++
											// Track file being analyzed
											pos := pass.Fset.Position(node.Pos())
											filesAnalyzed[pos.Filename] = true
										}
									}
								}
							}
						}
					}
				}
			}

		case *ast.AssignStmt:
			// Check each left-hand side of the assignment
			for _, lhs := range node.Lhs {
				// We're looking for field selections (e.g., event.Field)
				sel, ok := lhs.(*ast.SelectorExpr)
				if !ok {
					continue
				}

				// Get the identifier being selected from
				ident, ok := sel.X.(*ast.Ident)
				if !ok {
					continue
				}

				// Get the type of the selected expression
				selType := pass.TypesInfo.TypeOf(sel.X)
				if selType == nil {
					continue
				}

				// Handle pointer types
				if ptr, ok := selType.(*types.Pointer); ok {
					selType = ptr.Elem()
				}

				// Check if this is a named type
				named, ok := selType.(*types.Named)
				if !ok {
					continue
				}

				// Check if the type is from pkg/controller/events
				obj := named.Obj()
				if obj == nil || obj.Pkg() == nil {
					continue
				}

				pkgPath := obj.Pkg().Path()

				// Check if this is an event type from pkg/controller/events
				if !isEventPackage(pkgPath) {
					continue
				}

				// Check if this is actually a struct type
				if _, ok := named.Underlying().(*types.Struct); !ok {
					continue
				}

				// Skip if this assignment is within a method of the same type
				if currentRecvType != nil && types.Identical(currentRecvType, named) {
					continue
				}

				// Check if the identifier is a function parameter
				identObj := pass.TypesInfo.ObjectOf(ident)
				if identObj == nil {
					continue
				}

				varObj, ok := identObj.(*types.Var)
				if !ok {
					continue
				}

				// Only flag if this is a function parameter
				if !currentParams[varObj] {
					// Not a parameter, skip
					continue
				}

				// Report the violation
				violationsFound++
				pass.Reportf(node.Pos(),
					"event field mutation detected: event struct fields must not be modified after creation (type: %s, field: %s)",
					named.Obj().Name(),
					sel.Sel.Name,
				)
			}
		}

		return true
	})

	// Print diagnostic summary to stderr (only if we found event parameters to check)
	if eventParametersChecked > 0 {
		// Sort event types for consistent output
		eventTypeList := make([]string, 0, len(eventTypes))
		for eventType := range eventTypes {
			eventTypeList = append(eventTypeList, eventType)
		}
		sort.Strings(eventTypeList)

		fmt.Fprintf(os.Stderr, "Event immutability check [%s]:\n", pass.Pkg.Path())
		fmt.Fprintf(os.Stderr, "  Event types: %s\n", strings.Join(eventTypeList, ", "))
		fmt.Fprintf(os.Stderr, "  Files analyzed: %d\n", len(filesAnalyzed))
		fmt.Fprintf(os.Stderr, "  Parameters checked: %d\n", eventParametersChecked)

		if violationsFound > 0 {
			fmt.Fprintf(os.Stderr, "  ✗ Found %d violation(s)\n", violationsFound)
		} else {
			fmt.Fprintf(os.Stderr, "  ✓ No violations detected\n")
		}
	}

	return nil, nil
}

// isEventPackage checks if the package path is the controller events package.
func isEventPackage(pkgPath string) bool {
	// Match both the full path and relative path patterns
	return strings.HasSuffix(pkgPath, "/pkg/controller/events") ||
		strings.HasSuffix(pkgPath, "gitlab.com/haproxy-haptic/haptic/pkg/controller/events") ||
		pkgPath == "gitlab.com/haproxy-haptic/haptic/pkg/controller/events"
}
