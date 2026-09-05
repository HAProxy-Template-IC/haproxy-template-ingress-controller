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

func BenchmarkTextFragment3000Parts(b *testing.B) {
	benchmarkTextFragment(b, 3000)
}

func BenchmarkTextFragment100000Parts(b *testing.B) {
	benchmarkTextFragment(b, 100_000)
}

func benchmarkTextFragment(b *testing.B, size int) {
	b.Helper()
	parts := benchmarkTextFragmentParts(size)
	fragment, err := TextFragmentFromSorted(parts)
	if err != nil {
		b.Fatal(err)
	}
	joined, err := fragment.WithDelimiter("\n")
	if err != nil {
		b.Fatal(err)
	}
	if _, err := joined.String(); err != nil {
		b.Fatal(err)
	}
	pathKey := fmt.Sprintf("route-%06d", size-1)

	b.Run("cold-build", func(b *testing.B) {
		benchmarkTextFragmentColdBuild(b, parts)
	})
	b.Run("one-change", func(b *testing.B) {
		benchmarkTextFragmentOneChange(b, fragment, pathKey)
	})
	b.Run("one-change-apply", func(b *testing.B) {
		benchmarkTextFragmentOneChangeApply(b, fragment, pathKey)
	})
	b.Run("delimiter-view", func(b *testing.B) {
		benchmarkTextFragmentDelimiterView(b, fragment)
	})
	b.Run("warm-string", func(b *testing.B) {
		benchmarkTextFragmentWarmString(b, joined)
	})
}

func benchmarkTextFragmentColdBuild(b *testing.B, parts []TextPart) {
	b.Helper()
	b.ReportAllocs()
	for range b.N {
		built, err := TextFragmentFromSorted(parts)
		if err != nil {
			b.Fatal(err)
		}
		textFragmentSink = built
	}
}

func benchmarkTextFragmentOneChange(b *testing.B, fragment TextFragment, key string) {
	b.Helper()
	b.ReportAllocs()
	current := fragment
	for operation := range b.N {
		text := "zero"
		if operation&1 != 0 {
			text = "one"
		}
		var err error
		current, err = current.WithPart(key, text)
		if err != nil {
			b.Fatal(err)
		}
	}
	textFragmentSink = current
}

func benchmarkTextFragmentOneChangeApply(b *testing.B, fragment TextFragment, key string) {
	b.Helper()
	b.ReportAllocs()
	current := fragment
	for operation := range b.N {
		text := "zero"
		if operation&1 != 0 {
			text = "one"
		}
		var err error
		current, err = current.Apply([]TextFragmentChange{{Key: key, Text: text, Present: true}})
		if err != nil {
			b.Fatal(err)
		}
	}
	textFragmentSink = current
}

func benchmarkTextFragmentDelimiterView(b *testing.B, fragment TextFragment) {
	b.Helper()
	b.ReportAllocs()
	for range b.N {
		view, err := fragment.WithDelimiter("\n")
		if err != nil {
			b.Fatal(err)
		}
		textFragmentSink = view
	}
}

func benchmarkTextFragmentWarmString(b *testing.B, fragment TextFragment) {
	b.Helper()
	b.ReportAllocs()
	for range b.N {
		text, err := fragment.String()
		if err != nil {
			b.Fatal(err)
		}
		stringSink = text
	}
}

func BenchmarkTextFragmentHandle(b *testing.B) {
	fragment, err := TextFragmentFromSorted([]TextPart{{Key: "part", Text: "value"}})
	if err != nil {
		b.Fatal(err)
	}
	b.Run("copy", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			textFragmentSink = fragment
		}
	})
	b.Run("authentication", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			if err := fragment.ValidateAuthentication(); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func BenchmarkDocumentTextFragment3000Parts(b *testing.B) {
	fragment, err := TextFragmentFromSorted(benchmarkTextFragmentParts(3000))
	if err != nil {
		b.Fatal(err)
	}
	fragment, err = fragment.WithDelimiter("\n")
	if err != nil {
		b.Fatal(err)
	}
	previous := benchmarkTextFragmentDocument(b, nil, fragment)

	b.Run("cold-build", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			documentSink = benchmarkTextFragmentDocument(b, nil, fragment)
		}
	})
	b.Run("exact-reuse", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			documentSink = benchmarkTextFragmentDocument(b, &previous, fragment)
		}
	})
	b.Run("zero-byte-omit", func(b *testing.B) {
		b.ReportAllocs()
		empty, err := EmptyTextFragment().WithPart("present", "")
		if err != nil {
			b.Fatal(err)
		}
		for range b.N {
			var builder DocumentBuilder
			if err := builder.AppendTextFragment(empty); err != nil {
				b.Fatal(err)
			}
			document, err := builder.Build(nil)
			if err != nil {
				b.Fatal(err)
			}
			documentSink = document
		}
	})
}

func benchmarkTextFragmentParts(size int) []TextPart {
	parts := make([]TextPart, size)
	for index := range parts {
		parts[index] = TextPart{
			Key:  fmt.Sprintf("route-%06d", index),
			Text: fmt.Sprintf("route-%06d=value", index),
		}
	}
	return parts
}

func benchmarkTextFragmentDocument(b *testing.B, previous *Document, fragment TextFragment) Document {
	b.Helper()
	var builder DocumentBuilder
	if err := builder.AppendTextFragment(fragment); err != nil {
		b.Fatal(err)
	}
	document, err := builder.Build(previous)
	if err != nil {
		b.Fatal(err)
	}
	return document
}

var textFragmentSink TextFragment
