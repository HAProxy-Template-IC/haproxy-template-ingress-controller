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
	"gitlab.com/haproxy-haptic/scriggo/builtin"
	"gitlab.com/haproxy-haptic/scriggo/native"
)

// buildScriggoGlobals creates the global declarations for Scriggo templates.
// In Scriggo, filters become regular functions that templates can call.
//
// Example usage in Scriggo template:
//
//	{% for item := range sort_by(items, []string{"$.name"}) %}
//	{{ strip(item.value) }}
//	{% end %}
//
// The additionalDeclarations parameter allows callers to inject domain-specific type
// declarations without the templating package needing to know about those types.
// This maintains clean architecture by keeping templating independent of domain packages.
//
// Example:
//
//	additionalDecls := map[string]any{
//	    "currentConfig": (*parserconfig.StructuredConfig)(nil),
//	}
//	globals := buildScriggoGlobals(filters, funcs, additionalDecls)
func buildScriggoGlobals(customFilters map[string]FilterFunc, customFunctions map[string]GlobalFunc, additionalDeclarations map[string]any) native.Declarations {
	decl := native.Declarations{}

	// Register runtime context variables, custom functions, and builtins
	registerScriggoRuntimeVars(decl)
	registerScriggoCustomFunctions(decl)
	registerScriggoBuiltins(decl)

	// Register any custom filters passed in (wrapped for Scriggo)
	for name, filter := range customFilters {
		decl[name] = wrapFilterForScriggo(filter)
	}

	// Register any custom functions (except fail, which we handle specially)
	for name, fn := range customFunctions {
		if name == FuncFail {
			// Skip - we use scriggoFail instead of wrapped FailFunction
			continue
		}
		decl[name] = wrapFunctionForScriggo(fn)
	}

	// Register additional declarations from caller (domain-specific types)
	// These are typically nil pointers for Scriggo type registration
	for name, value := range additionalDeclarations {
		decl[name] = value
	}

	return decl
}

// registerScriggoRuntimeVars registers runtime context variables.
// These are declared with nil pointers so Scriggo knows the TYPE at compile time,
// but the VALUE is provided at runtime via template.Run(vars).
func registerScriggoRuntimeVars(decl native.Declarations) {
	decl["pathResolver"] = (*PathResolver)(nil)
	decl["resources"] = (*map[string]ResourceStore)(nil)
	decl["controller"] = (*map[string]ResourceStore)(nil)
	decl["templateSnippets"] = (*[]string)(nil)
	decl["fileRegistry"] = (*FileRegistrar)(nil)
	decl["dataplane"] = (*map[string]interface{})(nil)
	decl["capabilities"] = (*map[string]interface{})(nil)
	decl["shared"] = (*SharedContext)(nil)
	decl["extraContext"] = (*map[string]interface{})(nil)
	decl["http"] = (*HTTPFetcher)(nil)                      // HTTP store for fetching remote content
	decl["runtimeEnvironment"] = (*RuntimeEnvironment)(nil) // Runtime environment info (GOMAXPROCS, etc.)
	// Note: Domain-specific types like currentConfig are registered via additionalDeclarations
	// parameter in buildScriggoGlobals() to maintain clean architecture boundaries
}

// registerScriggoCustomFunctions registers all custom functions for Scriggo templates.
func registerScriggoCustomFunctions(decl native.Declarations) {
	// Custom filters as functions
	decl[FilterSortBy] = scriggoSortBy
	decl[FilterGlobMatch] = scriggoGlobMatch
	decl[FilterStrip] = scriggoStrip
	decl[FilterTrim] = scriggoTrim
	decl[FilterB64Decode] = scriggoB64Decode
	decl[FilterDebug] = scriggoDebug

	// Scriggo-specific fail function (uses native.Env)
	decl[FuncFail] = scriggoFail

	// Dict utility functions
	decl[FuncMerge] = scriggoMerge
	decl[FuncKeys] = scriggoKeys

	// Sorting functions
	decl[FuncSortStrings] = scriggoSortStrings

	// Deduplication and filtering functions
	decl[FuncFirstSeen] = scriggoFirstSeen
	decl[FuncSelectAttr] = scriggoSelectAttr
	decl[FuncJoinKey] = scriggoJoinKey

	// String manipulation functions (custom implementations)
	decl[FuncStringsContains] = scriggoStringsContains
	decl[FuncStringsSplit] = scriggoStringsSplit
	decl[FuncStringsTrim] = scriggoStringsTrim
	decl[FuncStringsLower] = scriggoStringsLower
	decl[FuncStringsReplace] = scriggoStringsReplace
	decl[FuncStringsSplitN] = scriggoStringsSplitN
	decl[FilterIndent] = scriggoIndent

	// Type conversion functions
	decl[FuncToString] = scriggoToString
	decl[FuncToInt] = scriggoToInt
	decl[FuncToFloat] = scriggoToFloat

	// Utility functions
	decl[FuncCeil] = scriggoCeil
	decl[FuncSeq] = scriggoSeq
	decl[FuncRegexSearch] = scriggoRegexSearch
	decl[FuncIsDigit] = scriggoIsDigit
	decl[FuncSanitizeRegex] = scriggoSanitizeRegex
	decl[FuncTitle] = scriggoTitle
	decl[FuncDig] = scriggoDig
	decl[FuncIsNil] = scriggoIsNil
	decl[FuncToStringSlice] = scriggoToStringSlice
	decl[FuncJoin] = scriggoJoin
	decl[FuncReplace] = scriggoStringsReplace

	// Namespace function for mutable state patterns
	decl[FuncNamespace] = scriggoNamespace
	decl[FuncCoalesce] = scriggoCoalesce
	decl[FuncFallback] = scriggoCoalesce // Jinja2-style alias

	// Slice manipulation functions
	decl[FuncToSlice] = scriggoToSlice
	decl[FuncAppendAny] = scriggoAppendAny
	decl[FuncShardSlice] = scriggoShardSlice

	// Path utility functions
	decl[funcBasename] = scriggoBasename
}

// registerScriggoBuiltins registers all Scriggo builtin functions.
func registerScriggoBuiltins(decl native.Declarations) {
	registerScriggoBuiltinCore(decl)
	registerScriggoBuiltinStrings(decl)
}

// registerScriggoBuiltinCore registers core Scriggo builtins (crypto, encoding, math, etc.).
func registerScriggoBuiltinCore(decl native.Declarations) {
	// crypto
	decl["hmacSHA1"] = builtin.HmacSHA1
	decl["hmacSHA256"] = builtin.HmacSHA256
	decl["sha1"] = builtin.Sha1
	decl["sha256"] = builtin.Sha256

	// encoding
	decl["base64"] = builtin.Base64
	decl["hex"] = builtin.Hex
	decl["marshalJSON"] = builtin.MarshalJSON
	decl["marshalJSONIndent"] = builtin.MarshalJSONIndent
	decl["marshalYAML"] = builtin.MarshalYAML
	decl["md5"] = builtin.Md5
	decl["unmarshalJSON"] = builtin.UnmarshalJSON
	decl["unmarshalYAML"] = builtin.UnmarshalYAML

	// html
	decl["htmlEscape"] = builtin.HtmlEscape

	// math
	decl["abs"] = builtin.Abs
	decl["max"] = builtin.Max
	decl["min"] = builtin.Min
	decl["pow"] = builtin.Pow

	// net
	decl["queryEscape"] = builtin.QueryEscape

	// regexp
	decl["regexp"] = builtin.RegExp

	// sort
	decl["reverse"] = builtin.Reverse

	// slice operations (batch processing for performance)
	decl["group_by"] = builtin.GroupBy
	decl["count_by"] = builtin.CountBy
	decl["index_by"] = builtin.IndexBy
	decl["unique_by"] = builtin.UniqueBy
	decl["map_extract"] = builtin.MapExtract

	// strconv
	decl["formatFloat"] = builtin.FormatFloat
	decl["formatInt"] = builtin.FormatInt
	decl["parseFloat"] = builtin.ParseFloat
	decl["parseInt"] = builtin.ParseInt

	// time
	decl["date"] = builtin.Date
	decl["now"] = builtin.Now
	decl["parseDuration"] = builtin.ParseDuration
	decl["parseTime"] = builtin.ParseTime
	decl["unixTime"] = builtin.UnixTime
}

// registerScriggoBuiltinStrings registers string-related Scriggo builtins.
func registerScriggoBuiltinStrings(decl native.Declarations) {
	decl["trimSpace"] = scriggoTrimSpace
	decl["strings_contains"] = scriggoStringsContains
	decl["abbreviate"] = builtin.Abbreviate
	decl["capitalize"] = builtin.Capitalize
	decl["capitalizeAll"] = builtin.CapitalizeAll
	decl["hasPrefix"] = builtin.HasPrefix
	decl["hasSuffix"] = builtin.HasSuffix
	decl["index"] = builtin.Index
	decl["indexAny"] = builtin.IndexAny
	decl["join"] = scriggoJoin // Override builtin to support []interface{} from append()
	decl["lastIndex"] = builtin.LastIndex
	decl["replace"] = scriggoStringsReplace // Override builtin to support 3-arg syntax (replaces all)
	decl["replaceAll"] = builtin.ReplaceAll
	decl["runeCount"] = builtin.RuneCount
	decl["split"] = builtin.Split
	decl["splitAfter"] = builtin.SplitAfter
	decl["splitAfterN"] = builtin.SplitAfterN
	decl["splitN"] = builtin.SplitN
	decl["sprint"] = builtin.Sprint
	decl["sprintf"] = builtin.Sprintf
	decl["toKebab"] = builtin.ToKebab
	decl["toLower"] = builtin.ToLower
	decl["toUpper"] = builtin.ToUpper
	decl["trim"] = builtin.Trim
	decl["trimLeft"] = builtin.TrimLeft
	decl["trimPrefix"] = builtin.TrimPrefix
	decl["trimRight"] = builtin.TrimRight
	decl["trimSuffix"] = builtin.TrimSuffix
}

// wrapFilterForScriggo wraps a FilterFunc to be callable from Scriggo templates.
// FilterFunc signature: func(in interface{}, args ...interface{}) (interface{}, error)
// Scriggo needs a concrete function signature, so we wrap it.
func wrapFilterForScriggo(filter FilterFunc) func(in interface{}, args ...interface{}) (interface{}, error) {
	return func(in interface{}, args ...interface{}) (interface{}, error) {
		return filter(in, args...)
	}
}

// wrapFunctionForScriggo wraps a GlobalFunc to be callable from Scriggo templates.
func wrapFunctionForScriggo(fn GlobalFunc) func(args ...interface{}) (interface{}, error) {
	return func(args ...interface{}) (interface{}, error) {
		return fn(args...)
	}
}
