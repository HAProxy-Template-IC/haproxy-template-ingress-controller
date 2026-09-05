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
	"errors"
	"fmt"
	"math"
	"reflect"
	"slices"
)

const (
	statusPatchProjectionNull byte = iota
	statusPatchProjectionBool
	statusPatchProjectionString
	statusPatchProjectionInt
	statusPatchProjectionUint
	statusPatchProjectionFloat
	statusPatchProjectionArray
	statusPatchProjectionObject
)

const (
	statusPatchProjectionNumberInt byte = iota
	statusPatchProjectionNumberInt8
	statusPatchProjectionNumberInt16
	statusPatchProjectionNumberInt32
	statusPatchProjectionNumberInt64
	statusPatchProjectionNumberUint
	statusPatchProjectionNumberUint8
	statusPatchProjectionNumberUint16
	statusPatchProjectionNumberUint32
	statusPatchProjectionNumberUint64
	statusPatchProjectionNumberFloat32
	statusPatchProjectionNumberFloat64
)

type statusPatchProjectionValue struct {
	kind       byte
	numberType byte
	boolean    bool
	text       string
	integer    int64
	unsigned   uint64
	floatBits  uint64
	array      []statusPatchProjectionValue
	object     []statusPatchProjectionField
}

type statusPatchProjectionField struct {
	name  string
	value statusPatchProjectionValue
}

type statusPatchProjectionVisit struct {
	kind byte
	ptr  uintptr
}

func newStatusPatchProjectionValue(
	value any,
	active map[statusPatchProjectionVisit]struct{},
	depth int,
) (statusPatchProjectionValue, error) {
	if depth > incrementalSerializationMaxDepth {
		return statusPatchProjectionValue{}, errors.New("value exceeds the incremental serialization depth limit")
	}
	switch typed := value.(type) {
	case nil:
		return statusPatchProjectionValue{kind: statusPatchProjectionNull}, nil
	case bool:
		return statusPatchProjectionValue{kind: statusPatchProjectionBool, boolean: typed}, nil
	case string:
		return statusPatchProjectionValue{kind: statusPatchProjectionString, text: typed}, nil
	case int:
		return statusPatchProjectionValue{
			kind: statusPatchProjectionInt, numberType: statusPatchProjectionNumberInt, integer: int64(typed),
		}, nil
	case int8:
		return statusPatchProjectionValue{
			kind: statusPatchProjectionInt, numberType: statusPatchProjectionNumberInt8, integer: int64(typed),
		}, nil
	case int16:
		return statusPatchProjectionValue{
			kind: statusPatchProjectionInt, numberType: statusPatchProjectionNumberInt16, integer: int64(typed),
		}, nil
	case int32:
		return statusPatchProjectionValue{
			kind: statusPatchProjectionInt, numberType: statusPatchProjectionNumberInt32, integer: int64(typed),
		}, nil
	case int64:
		return statusPatchProjectionValue{
			kind: statusPatchProjectionInt, numberType: statusPatchProjectionNumberInt64, integer: typed,
		}, nil
	case uint:
		return statusPatchProjectionValue{
			kind: statusPatchProjectionUint, numberType: statusPatchProjectionNumberUint, unsigned: uint64(typed),
		}, nil
	case uint8:
		return statusPatchProjectionValue{
			kind: statusPatchProjectionUint, numberType: statusPatchProjectionNumberUint8, unsigned: uint64(typed),
		}, nil
	case uint16:
		return statusPatchProjectionValue{
			kind: statusPatchProjectionUint, numberType: statusPatchProjectionNumberUint16, unsigned: uint64(typed),
		}, nil
	case uint32:
		return statusPatchProjectionValue{
			kind: statusPatchProjectionUint, numberType: statusPatchProjectionNumberUint32, unsigned: uint64(typed),
		}, nil
	case uint64:
		return statusPatchProjectionValue{
			kind: statusPatchProjectionUint, numberType: statusPatchProjectionNumberUint64, unsigned: typed,
		}, nil
	case float32:
		return newStatusPatchProjectionFloat(float64(typed), statusPatchProjectionNumberFloat32,
			uint64(math.Float32bits(typed)))
	case float64:
		return newStatusPatchProjectionFloat(typed, statusPatchProjectionNumberFloat64,
			math.Float64bits(typed))
	case []any:
		return newStatusPatchProjectionArray(typed, active, depth)
	case map[string]any:
		return newStatusPatchProjectionObject(typed, active, depth)
	default:
		return statusPatchProjectionValue{}, fmt.Errorf("value has unsupported type %T", value)
	}
}

func newStatusPatchProjectionFloat(
	value float64,
	numberType byte,
	bits uint64,
) (statusPatchProjectionValue, error) {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return statusPatchProjectionValue{}, errors.New("floating-point value is not finite")
	}
	return statusPatchProjectionValue{
		kind: statusPatchProjectionFloat, numberType: numberType, floatBits: bits,
	}, nil
}

func newStatusPatchProjectionArray(
	typed []any,
	active map[statusPatchProjectionVisit]struct{},
	depth int,
) (statusPatchProjectionValue, error) {
	marker := statusPatchProjectionVisit{kind: statusPatchProjectionArray, ptr: reflect.ValueOf(typed).Pointer()}
	if marker.ptr != 0 {
		if _, exists := active[marker]; exists {
			return statusPatchProjectionValue{}, errors.New("value contains a reference cycle")
		}
		active[marker] = struct{}{}
		defer delete(active, marker)
	}
	array := make([]statusPatchProjectionValue, len(typed))
	for index := range typed {
		projected, err := newStatusPatchProjectionValue(typed[index], active, depth+1)
		if err != nil {
			return statusPatchProjectionValue{}, fmt.Errorf("index %d: %w", index, err)
		}
		array[index] = projected
	}
	return statusPatchProjectionValue{kind: statusPatchProjectionArray, array: array}, nil
}

func newStatusPatchProjectionObject(
	typed map[string]any,
	active map[statusPatchProjectionVisit]struct{},
	depth int,
) (statusPatchProjectionValue, error) {
	marker := statusPatchProjectionVisit{kind: statusPatchProjectionObject, ptr: reflect.ValueOf(typed).Pointer()}
	if marker.ptr != 0 {
		if _, exists := active[marker]; exists {
			return statusPatchProjectionValue{}, errors.New("value contains a reference cycle")
		}
		active[marker] = struct{}{}
		defer delete(active, marker)
	}
	names := make([]string, 0, len(typed))
	for name := range typed {
		names = append(names, name)
	}
	slices.Sort(names)
	object := make([]statusPatchProjectionField, len(names))
	for index, name := range names {
		projected, err := newStatusPatchProjectionValue(typed[name], active, depth+1)
		if err != nil {
			return statusPatchProjectionValue{}, fmt.Errorf("field %q: %w", name, err)
		}
		object[index] = statusPatchProjectionField{name: name, value: projected}
	}
	return statusPatchProjectionValue{kind: statusPatchProjectionObject, object: object}, nil
}

func (v *statusPatchProjectionValue) materializeObject() (map[string]any, error) {
	var value any
	if err := v.materializeInto(&value); err != nil {
		return nil, err
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("projected variant has type %T", value)
	}
	return object, nil
}

func (v *statusPatchProjectionValue) materializeInto(destination *any) error {
	switch v.kind {
	case statusPatchProjectionNull:
		*destination = nil
		return nil
	case statusPatchProjectionBool:
		*destination = v.boolean
		return nil
	case statusPatchProjectionString:
		*destination = v.text
		return nil
	case statusPatchProjectionInt:
		return materializeNumberInto(destination, v.materializeInt)
	case statusPatchProjectionUint:
		return materializeNumberInto(destination, v.materializeUint)
	case statusPatchProjectionFloat:
		return materializeNumberInto(destination, v.materializeFloat)
	case statusPatchProjectionArray:
		result := make([]any, len(v.array))
		for index := range v.array {
			if err := v.array[index].materializeInto(&result[index]); err != nil {
				return fmt.Errorf("index %d: %w", index, err)
			}
		}
		*destination = result
		return nil
	case statusPatchProjectionObject:
		result := make(map[string]any, len(v.object))
		for index := range v.object {
			field := &v.object[index]
			var value any
			if err := field.value.materializeInto(&value); err != nil {
				return fmt.Errorf("field %q: %w", field.name, err)
			}
			result[field.name] = value
		}
		*destination = result
		return nil
	default:
		return fmt.Errorf("projected variant has invalid kind %d", v.kind)
	}
}

func materializeNumberInto(destination *any, materialize func() (any, error)) error {
	value, err := materialize()
	if err != nil {
		return err
	}
	*destination = value
	return nil
}

func (v *statusPatchProjectionValue) materializeInt() (any, error) {
	switch v.numberType {
	case statusPatchProjectionNumberInt:
		return int(v.integer), nil
	case statusPatchProjectionNumberInt8:
		if v.integer < math.MinInt8 || v.integer > math.MaxInt8 {
			return nil, errors.New("projected variant overflows int8")
		}
		return int8(v.integer), nil
	case statusPatchProjectionNumberInt16:
		if v.integer < math.MinInt16 || v.integer > math.MaxInt16 {
			return nil, errors.New("projected variant overflows int16")
		}
		return int16(v.integer), nil
	case statusPatchProjectionNumberInt32:
		if v.integer < math.MinInt32 || v.integer > math.MaxInt32 {
			return nil, errors.New("projected variant overflows int32")
		}
		return int32(v.integer), nil
	case statusPatchProjectionNumberInt64:
		return v.integer, nil
	default:
		return nil, errors.New("projected variant has an invalid signed number type")
	}
}

func (v *statusPatchProjectionValue) materializeUint() (any, error) {
	switch v.numberType {
	case statusPatchProjectionNumberUint:
		return uint(v.unsigned), nil
	case statusPatchProjectionNumberUint8:
		if v.unsigned > math.MaxUint8 {
			return nil, errors.New("projected variant overflows uint8")
		}
		return uint8(v.unsigned), nil
	case statusPatchProjectionNumberUint16:
		if v.unsigned > math.MaxUint16 {
			return nil, errors.New("projected variant overflows uint16")
		}
		return uint16(v.unsigned), nil
	case statusPatchProjectionNumberUint32:
		if v.unsigned > math.MaxUint32 {
			return nil, errors.New("projected variant overflows uint32")
		}
		return uint32(v.unsigned), nil
	case statusPatchProjectionNumberUint64:
		return v.unsigned, nil
	default:
		return nil, errors.New("projected variant has an invalid unsigned number type")
	}
}

func (v *statusPatchProjectionValue) materializeFloat() (any, error) {
	switch v.numberType {
	case statusPatchProjectionNumberFloat32:
		if v.floatBits > math.MaxUint32 {
			return nil, errors.New("projected variant overflows float32 bits")
		}
		return math.Float32frombits(uint32(v.floatBits)), nil
	case statusPatchProjectionNumberFloat64:
		return math.Float64frombits(v.floatBits), nil
	default:
		return nil, errors.New("projected variant has an invalid floating-point number type")
	}
}
