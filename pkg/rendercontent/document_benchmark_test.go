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

package rendercontent

import (
	"fmt"
	"testing"
)

func BenchmarkDocument3000Outputs(b *testing.B) {
	outputs := make([]Output, 3000)
	for index := range outputs {
		var err error
		outputs[index], err = FromSorted([]Change{{
			Key: "part", Text: fmt.Sprintf("route-%06d=value\n", index),
		}})
		if err != nil {
			b.Fatal(err)
		}
	}
	previous := benchmarkDocument(b, nil, outputs)
	if _, err := previous.String(); err != nil {
		b.Fatal(err)
	}

	b.Run("cold-build", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			documentSink = benchmarkDocument(b, nil, outputs)
		}
	})
	b.Run("exact-reuse", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			documentSink = benchmarkDocument(b, &previous, outputs)
		}
	})
	b.Run("warm-string", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			text, err := previous.String()
			if err != nil {
				b.Fatal(err)
			}
			stringSink = text
		}
	})
}

func BenchmarkDocumentHandle(b *testing.B) {
	document := benchmarkDocument(b, nil, nil)
	b.Run("copy", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			documentSink = document
		}
	})
	b.Run("authentication", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			if err := document.ValidateAuthentication(); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func benchmarkDocument(b *testing.B, previous *Document, outputs []Output) Document {
	b.Helper()
	var builder DocumentBuilder
	for _, output := range outputs {
		if err := builder.AppendOutput(output); err != nil {
			b.Fatal(err)
		}
	}
	document, err := builder.Build(previous)
	if err != nil {
		b.Fatal(err)
	}
	return document
}

var documentSink Document
