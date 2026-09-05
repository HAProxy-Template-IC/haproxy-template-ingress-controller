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

package rendercontext

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"gitlab.com/haproxy-haptic/haptic/pkg/dataplane/renderplan"
	"gitlab.com/haproxy-haptic/haptic/pkg/templating"
)

var planAssemblyConfigSink string
var planAssemblySectionsSink []renderplan.Section

func BenchmarkPlanAssembly(b *testing.B) {
	for _, sections := range []int{300, 3000} {
		b.Run(fmt.Sprintf("sections=%d/raw", sections), func(b *testing.B) {
			benchmarkPlanAssembly(b, sections, nil, nil)
		})
		b.Run(fmt.Sprintf("sections=%d/post-processed", sections), func(b *testing.B) {
			post, _ := benchmarkPlanPostProcessors(b)
			benchmarkPlanAssembly(b, sections, post, nil)
		})
		b.Run(fmt.Sprintf("sections=%d/post-processed-batch", sections), func(b *testing.B) {
			post, batch := benchmarkPlanPostProcessors(b)
			benchmarkPlanAssembly(b, sections, post, batch)
		})
	}
}

func benchmarkPlanAssembly(
	b *testing.B,
	sectionCount int,
	post PostProcessFunc,
	postBatch PostProcessBatchFunc,
) {
	b.Helper()
	registry := NewPlanRegistry(nil)
	var rendered strings.Builder
	rendered.WriteString("global\n  daemon\n")
	for index := range sectionCount {
		name := fmt.Sprintf("backend-%06d", index)
		text := fmt.Sprintf(
			"    backend %s\n        server server-%06d 10.0.%d.%d:8080\n\n\n",
			name,
			index,
			(index>>8)&255,
			index&255,
		)
		token, err := registry.Section(renderplan.SectionKindBackend, name, text)
		if err != nil {
			b.Fatal(err)
		}
		rendered.WriteString(token)
	}
	rendered.WriteString("frontend http\n  bind :80\n")
	input := rendered.String()
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		config, sections, err := registry.AssembleWithBatch(ctx, input, post, postBatch)
		if err != nil {
			b.Fatal(err)
		}
		planAssemblyConfigSink = config
		planAssemblySectionsSink = sections
	}
}

func benchmarkPlanPostProcessors(b *testing.B) (PostProcessFunc, PostProcessBatchFunc) {
	b.Helper()
	engine, err := templating.New(
		map[string]string{"main": ""},
		&templating.Options{
			EntryPoints: []string{"main"},
			PostProcessors: map[string][]templating.PostProcessorConfig{
				"main": {
					{Type: templating.PostProcessorTypeRegexReplace, Params: map[string]string{
						"pattern": "^[ ]+",
						"replace": "  ",
					}},
					{Type: templating.PostProcessorTypeTemplate, Params: map[string]string{
						"source": `{{- regexp("\\n([ \\t]*\\n)+").ReplaceAll(input, "\n\n") -}}`,
					}},
				},
			},
		},
	)
	if err != nil {
		b.Fatal(err)
	}
	return func(ctx context.Context, text string) (string, error) {
			return engine.PostProcess(ctx, "main", text)
		}, func(ctx context.Context, texts []string) ([]string, error) {
			return engine.PostProcessBatch(ctx, "main", texts)
		}
}
