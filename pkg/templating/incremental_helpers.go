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

package templating

import (
	"errors"
	"fmt"
	"math"
	pathpkg "path"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"gitlab.com/haproxy-haptic/scriggo/builtin"
	"gitlab.com/haproxy-haptic/scriggo/native"
)

func incrementalStop(env native.Env, name string, err error) {
	env.Stop(fmt.Errorf("%s: %w", name, err))
}

func incrementalStringOrStop(env native.Env, name string, value any) string {
	scalar, err := deterministicScalarOf(value)
	if err != nil {
		incrementalStop(env, name, err)
		return ""
	}
	return scalar.text
}

func incrementalToString(env native.Env, value any) string {
	return incrementalStringOrStop(env, FuncToString, value)
}

func incrementalStrip(env native.Env, value any) string {
	return strip(incrementalStringOrStop(env, FilterStrip, value))
}

func incrementalB64Decode(value any) (string, error) {
	scalar, err := deterministicScalarOf(value)
	if err != nil {
		return "", fmt.Errorf("%s: %w", FilterB64Decode, err)
	}
	return scriggoB64Decode(scalar.text)
}

func incrementalB64Encode(env native.Env, value any) string {
	return scriggoB64Encode(incrementalStringOrStop(env, FilterB64Encode, value))
}

func incrementalIndent(value any, args ...any) (string, error) {
	scalar, err := deterministicScalarOf(value)
	if err != nil {
		return "", fmt.Errorf("%s: %w", FilterIndent, err)
	}
	return scriggoIndent(scalar.text, args...)
}

func incrementalStringsContains(env native.Env, value, substring any) bool {
	left := incrementalStringOrStop(env, FuncStringsContains, value)
	right := incrementalStringOrStop(env, FuncStringsContains, substring)
	return strings.Contains(left, right)
}

func incrementalStringsSplit(env native.Env, value, separator any) []string {
	left := incrementalStringOrStop(env, FuncStringsSplit, value)
	right := incrementalStringOrStop(env, FuncStringsSplit, separator)
	return strings.Split(left, right)
}

func incrementalStringsTrim(env native.Env, value any) string {
	return strings.TrimSpace(incrementalStringOrStop(env, FuncStringsTrim, value))
}

func incrementalStringsLower(env native.Env, value any) string {
	return strings.ToLower(incrementalStringOrStop(env, FuncStringsLower, value))
}

func incrementalStringsReplace(env native.Env, value, old, replacement any) string {
	input := incrementalStringOrStop(env, FuncStringsReplace, value)
	oldString := incrementalStringOrStop(env, FuncStringsReplace, old)
	replacementString := incrementalStringOrStop(env, FuncStringsReplace, replacement)
	return strings.ReplaceAll(input, oldString, replacementString)
}

func incrementalStringsSplitN(env native.Env, value, separator any, count int) []string {
	input := incrementalStringOrStop(env, FuncStringsSplitN, value)
	separatorString := incrementalStringOrStop(env, FuncStringsSplitN, separator)
	return strings.SplitN(input, separatorString, count)
}

func incrementalTitle(env native.Env, value any) string {
	return scriggoTitle(incrementalStringOrStop(env, FuncTitle, value))
}

func incrementalSanitizeRegex(env native.Env, value any) string {
	return scriggoSanitizeRegex(incrementalStringOrStop(env, FuncSanitizeRegex, value))
}

func incrementalIsDigit(env native.Env, value any) bool {
	return scriggoIsDigit(incrementalStringOrStop(env, FuncIsDigit, value))
}

func newIncrementalRegexSearch() func(native.Env, any, any) bool {
	search := newScriggoRegexSearch()
	return func(env native.Env, value, pattern any) bool {
		input := incrementalStringOrStop(env, FuncRegexSearch, value)
		patternString := incrementalStringOrStop(env, FuncRegexSearch, pattern)
		return search(env, input, patternString)
	}
}

func incrementalGlobMatch(env native.Env, items any, pattern string) []string {
	if items == nil || pattern == "" {
		return []string{}
	}
	rv := reflect.ValueOf(items)
	if rv.Kind() != reflect.Slice {
		incrementalStop(env, FilterGlobMatch, fmt.Errorf("expected a slice, got %T", items))
		return nil
	}
	result := make([]string, 0, rv.Len())
	for index := range rv.Len() {
		name, found, err := incrementalGlobName(rv.Index(index).Interface())
		if err != nil {
			incrementalStop(env, FilterGlobMatch, fmt.Errorf("item at index %d: %w", index, err))
			return nil
		}
		if !found {
			continue
		}
		matched, err := pathpkg.Match(pattern, name)
		if err != nil {
			incrementalStop(env, FilterGlobMatch, fmt.Errorf("invalid pattern %q: %w", pattern, err))
			return nil
		}
		if matched {
			result = append(result, name)
		}
	}
	return result
}

func incrementalGlobName(value any) (name string, found bool, err error) {
	if name, ok := value.(string); ok {
		return name, true, nil
	}
	nameValue, found, err := incrementalField(value, "name")
	if err != nil || !found {
		return "", found, err
	}
	scalar, err := deterministicScalarOf(nameValue)
	if err != nil {
		return "", false, err
	}
	if scalar.kind != deterministicStringScalar {
		return "", false, fmt.Errorf("name must be a string, got %T", nameValue)
	}
	return scalar.text, true, nil
}

func incrementalDigString(env native.Env, object any, defaultValue string, keys ...string) string {
	value := incrementalDig(env, object, keys...)
	if value == nil {
		return defaultValue
	}
	return incrementalStringOrStop(env, FuncDigString, value)
}

func incrementalDig(env native.Env, object any, keys ...string) any {
	value, found, err := incrementalDigValue(object, keys)
	if err != nil {
		incrementalStop(env, FuncDig, err)
		return nil
	}
	if !found {
		return nil
	}
	return value
}

func incrementalDigValue(object any, keys []string) (value any, found bool, err error) {
	var current reflect.Value
	if object != nil {
		current = reflect.ValueOf(object)
	}
	for _, key := range keys {
		next, nextFound, fieldErr := incrementalFieldValue(current, key)
		if fieldErr != nil {
			return nil, false, fieldErr
		}
		if !nextFound {
			return nil, false, nil
		}
		current = next
	}
	if !current.IsValid() || current.Kind() == reflect.Interface && current.IsNil() {
		return nil, true, nil
	}
	return current.Interface(), true, nil
}

func incrementalJSONPathGet(env native.Env, item any, path string) any {
	segments, err := parseConcreteJSONPath(path)
	if err != nil {
		incrementalStop(env, FuncJSONPathGet, err)
		return nil
	}
	current := item
	for _, segment := range segments {
		if current == nil {
			return nil
		}
		if segment.isIndex {
			var found bool
			current, found = incrementalIndex(current, segment.index)
			if !found {
				return nil
			}
			continue
		}
		var found bool
		current, found, err = incrementalField(current, segment.key)
		if err != nil {
			incrementalStop(env, FuncJSONPathGet, err)
			return nil
		}
		if !found {
			return nil
		}
	}
	return current
}

func incrementalStringSlice(env native.Env, name string, items any) []string {
	if items == nil {
		return []string{}
	}
	rv := reflect.ValueOf(items)
	if rv.Kind() != reflect.Slice {
		incrementalStop(env, name, fmt.Errorf("expected a slice, got %T", items))
		return nil
	}
	result := make([]string, rv.Len())
	for i := range rv.Len() {
		result[i] = incrementalStringOrStop(env, name, rv.Index(i).Interface())
	}
	return result
}

func incrementalToStringSlice(env native.Env, items any) []string {
	return incrementalStringSlice(env, FuncToStringSlice, items)
}

func incrementalSortStrings(env native.Env, items []any) []string {
	result := incrementalStringSlice(env, FuncSortStrings, items)
	slices.Sort(result)
	return result
}

func incrementalJoin(env native.Env, items any, separator string) string {
	return strings.Join(incrementalStringSlice(env, FuncJoin, items), separator)
}

func incrementalJoinKey(env native.Env, separator string, parts ...any) string {
	stringsByPart := make([]string, len(parts))
	for i, part := range parts {
		stringsByPart[i] = incrementalStringOrStop(env, FuncJoinKey, part)
	}
	return strings.Join(stringsByPart, separator)
}

func incrementalMakeGUID(env native.Env, parts ...any) string {
	stringsByPart := make([]string, len(parts))
	for i, part := range parts {
		stringsByPart[i] = incrementalStringOrStop(env, FuncMakeGUID, part)
	}
	guid := strings.Join(stringsByPart, ":")
	if len(guid) <= haproxyGUIDMaxLen {
		return guid
	}
	return truncateGUID(guid)
}

func incrementalToStrMap(env native.Env, items any) map[string]string {
	if items == nil {
		return nil
	}
	rv := reflect.ValueOf(items)
	if rv.Kind() != reflect.Map || rv.Type().Key().Kind() != reflect.String {
		incrementalStop(env, FuncToStrMap, fmt.Errorf("expected a string-keyed map, got %T", items))
		return nil
	}
	result := make(map[string]string, rv.Len())
	for iterator := rv.MapRange(); iterator.Next(); {
		result[iterator.Key().String()] = incrementalStringOrStop(env, FuncToStrMap, iterator.Value().Interface())
	}
	return result
}

func incrementalKeys(env native.Env, dictionary any) []string {
	if dictionary == nil {
		return []string{}
	}
	rv := reflect.ValueOf(dictionary)
	if rv.Kind() != reflect.Map || rv.Type().Key().Kind() != reflect.String {
		incrementalStop(env, FuncKeys, fmt.Errorf("expected a string-keyed map, got %T", dictionary))
		return nil
	}
	keys := make([]string, 0, rv.Len())
	for iterator := rv.MapRange(); iterator.Next(); {
		keys = append(keys, iterator.Key().String())
	}
	slices.Sort(keys)
	return keys
}

func incrementalMapExtract(env native.Env, items any, keyPath string) []any {
	if items == nil {
		return []any{}
	}
	rv := reflect.ValueOf(items)
	if rv.Kind() != reflect.Slice {
		incrementalStop(env, builtinMapExtract, fmt.Errorf("expected a slice, got %T", items))
		return nil
	}
	var keys []string
	if keyPath != "" {
		keys = strings.Split(keyPath, ".")
	}
	result := make([]any, rv.Len())
	for index := range rv.Len() {
		value, found, err := incrementalDigValue(rv.Index(index).Interface(), keys)
		if err != nil {
			incrementalStop(env, builtinMapExtract, fmt.Errorf("item at index %d: %w", index, err))
			return nil
		}
		if !found {
			value = nil
		}
		result[index] = value
	}
	return result
}

func incrementalSemverGte(env native.Env, version, minimum any) bool {
	versionString := incrementalStringOrStop(env, FuncSemverGte, version)
	minimumString := incrementalStringOrStop(env, FuncSemverGte, minimum)
	return scriggoSemverGte(versionString, minimumString)
}

func incrementalToInt(env native.Env, value any) int {
	scalar, err := deterministicScalarOf(value)
	if err != nil {
		incrementalStop(env, FuncToInt, err)
		return 0
	}
	result, err := deterministicScalarInt(scalar)
	if err != nil {
		incrementalStop(env, FuncToInt, err)
		return 0
	}
	return result
}

func deterministicScalarInt(value deterministicScalar) (int, error) {
	switch value.kind {
	case deterministicNilScalar, deterministicBoolScalar:
		return 0, nil
	case deterministicStringScalar:
		parsed, ok := incrementalParseInt(value.text)
		if !ok {
			return 0, nil
		}
		return parsed, nil
	case deterministicSignedScalar:
		converted := int(value.signed)
		if int64(converted) != value.signed {
			return 0, fmt.Errorf("integer %s does not fit in int", value.text)
		}
		return converted, nil
	case deterministicUnsignedScalar:
		if value.unsigned > uint64(math.MaxInt) {
			return 0, fmt.Errorf("integer %s does not fit in int", value.text)
		}
		converted := int(value.unsigned)
		return converted, nil
	case deterministicFloatScalar:
		lowerBound := float64(math.MinInt)
		if value.floating < lowerBound || value.floating >= -lowerBound {
			return 0, fmt.Errorf("number %s does not fit in int", value.text)
		}
		return int(value.floating), nil
	default:
		panic("templating: unknown incremental scalar")
	}
}

func incrementalParseInt(value string) (parsed int, valid bool) {
	parsed, err := strconv.Atoi(value)
	return parsed, err == nil
}

func incrementalSortInts(env native.Env, items []any) []int {
	result := make([]int, len(items))
	for i, item := range items {
		result[i] = incrementalToInt(env, item)
	}
	slices.Sort(result)
	return result
}

func incrementalToFloat(value any) (float64, error) {
	scalar, err := deterministicScalarOf(value)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", FuncToFloat, err)
	}
	switch scalar.kind {
	case deterministicNilScalar:
		return 0, nil
	case deterministicSignedScalar:
		return float64(scalar.signed), nil
	case deterministicUnsignedScalar:
		return float64(scalar.unsigned), nil
	case deterministicFloatScalar:
		return scalar.floating, nil
	case deterministicStringScalar:
		parsed, parseErr := strconv.ParseFloat(scalar.text, 64)
		if parseErr != nil {
			return 0, parseErr
		}
		if math.IsNaN(parsed) || math.IsInf(parsed, 0) {
			return 0, fmt.Errorf("non-finite float is unavailable in incremental templates")
		}
		return parsed, nil
	default:
		return 0, fmt.Errorf("converting %s to float", scalar.text)
	}
}

func incrementalCeil(env native.Env, value float64) float64 {
	if err := incrementalFiniteFloat(value); err != nil {
		incrementalStop(env, FuncCeil, err)
		return 0
	}
	return math.Ceil(value)
}

func incrementalFormatFloat(env native.Env, value float64, format string, precision int) string {
	if err := incrementalFiniteFloat(value); err != nil {
		incrementalStop(env, builtinFormatFloat, err)
		return ""
	}
	return builtin.FormatFloat(value, format, precision)
}

func incrementalPow(env native.Env, base, exponent float64) float64 {
	if err := incrementalFiniteFloat(base); err != nil {
		incrementalStop(env, builtinPow, err)
		return 0
	}
	if err := incrementalFiniteFloat(exponent); err != nil {
		incrementalStop(env, builtinPow, err)
		return 0
	}
	result := builtin.Pow(base, exponent)
	if err := incrementalFiniteFloat(result); err != nil {
		incrementalStop(env, builtinPow, err)
		return 0
	}
	return result
}

func incrementalFiniteFloat(value float64) error {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return fmt.Errorf("non-finite float is unavailable in incremental templates")
	}
	return nil
}

func incrementalDate(
	env native.Env,
	year, month, day, hour, minute, second, nanosecond int,
	location string,
) builtin.Time {
	if location != "" && location != "UTC" {
		incrementalStop(env, builtinDate, errors.New("incremental templates only support UTC"))
		return builtin.Time{}
	}
	value := time.Date(year, time.Month(month), day, hour, minute, second, nanosecond, time.UTC)
	return builtin.NewTime(value)
}

func incrementalUnixTime(seconds, nanoseconds int64) builtin.Time {
	return builtin.UnixTime(seconds, nanoseconds).UTC()
}

func incrementalUntarGz(archive string) (map[string]string, error) {
	return untarGz(archive, archiveLimits{
		maxEntries:    4096,
		maxEntryBytes: 8 << 20,
		maxTotalBytes: 32 << 20,
	})
}

func incrementalParseTime(env native.Env, layout, value string) builtin.Time {
	if layout == "" {
		incrementalStop(env, builtinParseTime, errors.New("incremental templates require an explicit layout"))
		return builtin.Time{}
	}
	parsed, err := time.ParseInLocation(layout, value, time.UTC)
	if err != nil {
		incrementalStop(env, builtinParseTime, err)
		return builtin.Time{}
	}
	return builtin.NewTime(parsed)
}

func incrementalCondition(
	env native.Env,
	conditionType, status, reason, message string,
	observedGeneration any,
	lastTransitionTime string,
) map[string]any {
	scalar, err := deterministicScalarOf(observedGeneration)
	if err != nil {
		incrementalStop(env, FuncCondition, err)
		return nil
	}
	generation, err := incrementalGeneration(scalar)
	if err != nil {
		incrementalStop(env, FuncCondition, err)
		return nil
	}
	return map[string]any{
		incrementalTypeField: conditionType,
		"status":             status,
		"reason":             reason,
		"message":            message,
		"observedGeneration": generation,
		"lastTransitionTime": lastTransitionTime,
	}
}

func incrementalGeneration(value deterministicScalar) (int64, error) {
	switch value.kind {
	case deterministicNilScalar, deterministicBoolScalar, deterministicStringScalar:
		return 0, nil
	case deterministicSignedScalar:
		return value.signed, nil
	case deterministicUnsignedScalar:
		if value.unsigned > math.MaxInt64 {
			return 0, fmt.Errorf("observed generation %s does not fit in int64", value.text)
		}
		return int64(value.unsigned), nil
	case deterministicFloatScalar:
		rounded := math.Round(value.floating)
		if rounded < math.MinInt64 || rounded >= -float64(math.MinInt64) {
			return 0, fmt.Errorf("observed generation %s does not fit in int64", value.text)
		}
		return int64(rounded), nil
	default:
		panic("templating: unknown incremental scalar")
	}
}

func incrementalToJSON(env native.Env, value any) string {
	detached, err := cloneIncrementalSerialization(value)
	if err != nil {
		incrementalStop(env, FilterToJSON, err)
		return ""
	}
	data, err := builtin.MarshalJSON(detached)
	if err != nil {
		incrementalStop(env, FilterToJSON, err)
		return ""
	}
	return string(data)
}

func incrementalMarshalJSON(env native.Env, value any) native.JSON {
	detached, err := cloneIncrementalSerialization(value)
	if err != nil {
		incrementalStop(env, builtinMarshalJSON, err)
		return ""
	}
	data, err := builtin.MarshalJSON(detached)
	if err != nil {
		incrementalStop(env, builtinMarshalJSON, err)
		return ""
	}
	return data
}

func incrementalMarshalJSONIndent(env native.Env, value any, prefix, indent string) native.JSON {
	detached, err := cloneIncrementalSerialization(value)
	if err != nil {
		incrementalStop(env, builtinMarshalJSONIndent, err)
		return ""
	}
	data, err := builtin.MarshalJSONIndent(detached, prefix, indent)
	if err != nil {
		incrementalStop(env, builtinMarshalJSONIndent, err)
		return ""
	}
	return data
}

func incrementalMarshalYAML(env native.Env, value any) string {
	detached, err := cloneIncrementalSerialization(value)
	if err != nil {
		incrementalStop(env, builtinMarshalYAML, err)
		return ""
	}
	data, err := builtin.MarshalYAML(detached)
	if err != nil {
		incrementalStop(env, builtinMarshalYAML, err)
		return ""
	}
	return data
}
