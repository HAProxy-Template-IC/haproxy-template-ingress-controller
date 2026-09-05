package templating

import (
	"reflect"
	"sort"
	"testing"

	"gitlab.com/haproxy-haptic/scriggo/native"
)

func TestIncrementalNativeFrameSignatureProbe(t *testing.T) {
	globals := buildScriggoIncrementalGlobals(nil, nil)
	names := make([]string, 0, len(globals))
	for name := range globals {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		value := globals[name]
		if synchronous, ok := value.(native.SynchronousDeclaration); ok {
			value = synchronous.Declaration
		}
		if adaptive, ok := value.(native.AdaptiveFunc); ok {
			value = adaptive.Impl
		}
		typ := reflect.TypeOf(value)
		if typ != nil && typ.Kind() == reflect.Func {
			t.Logf("%s: %s direct=%t", name, typ, makeIncrementalNativeFunctionSignatureFrameTrampoline(value) != nil)
		}
	}
}

func TestIncrementalRootPrimitivesHaveDirectFrames(t *testing.T) {
	functions := map[string]any{
		FuncIncrementalRender:                 scriggoIncrementalRender,
		FuncIncrementalValues:                 scriggoIncrementalValues,
		FuncIncrementalRankedFragments:        scriggoIncrementalRankedFragments,
		FuncIncrementalRankedFragmentsJoin:    scriggoIncrementalRankedFragmentsJoin,
		FuncIncrementalRankedTextFragment:     scriggoIncrementalRankedTextFragment,
		FuncIncrementalRankedTextFragmentJoin: scriggoIncrementalRankedTextFragmentJoin,
	}
	for name, function := range functions {
		t.Run(name, func(t *testing.T) {
			trampoline := makeIncrementalNativeFunctionSignatureFrameTrampoline(function)
			if trampoline == nil || !trampoline.SupportsFunctionCallFrame() {
				t.Fatal("incremental root primitive has no direct frame")
			}
			if !incrementalNativeFunctionHasFrame(incrementalNativeFunctionFrameTrampolines, reflect.ValueOf(function)) {
				t.Fatal("incremental root primitive direct frame is not registered")
			}
		})
	}
}
