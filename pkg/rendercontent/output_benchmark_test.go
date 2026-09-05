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

func BenchmarkOutput3000Parts(b *testing.B) {
	parts := make([]Change, 3000)
	for index := range parts {
		parts[index] = Change{
			Key:  fmt.Sprintf("route-%06d", index),
			Text: fmt.Sprintf("route-%06d=value\n", index),
		}
	}
	output, err := FromSorted(parts)
	if err != nil {
		b.Fatal(err)
	}
	if _, err := output.String(); err != nil {
		b.Fatal(err)
	}

	b.Run("cold-build", func(b *testing.B) {
		benchmarkOutputColdBuild(b, parts)
	})
	b.Run("one-change", func(b *testing.B) {
		benchmarkOutputOneChange(b, output)
	})
	b.Run("warm-string", func(b *testing.B) {
		benchmarkOutputWarmString(b, output)
	})
}

func benchmarkOutputColdBuild(b *testing.B, parts []Change) {
	b.Helper()
	b.ReportAllocs()
	for range b.N {
		built, err := FromSorted(parts)
		if err != nil {
			b.Fatal(err)
		}
		outputSink = built
	}
}

func benchmarkOutputOneChange(b *testing.B, output Output) {
	b.Helper()
	b.ReportAllocs()
	current := output
	for operation := range b.N {
		text := "route-001500=zero\n"
		if operation&1 != 0 {
			text = "route-001500=one\n"
		}
		var err error
		current, err = current.WithText("route-001500", text)
		if err != nil {
			b.Fatal(err)
		}
	}
	outputSink = current
}

func benchmarkOutputWarmString(b *testing.B, output Output) {
	b.Helper()
	b.ReportAllocs()
	for range b.N {
		text, err := output.String()
		if err != nil {
			b.Fatal(err)
		}
		stringSink = text
	}
}

func BenchmarkOutputHandle(b *testing.B) {
	output, err := FromSorted([]Change{{Key: "part", Text: "value"}})
	if err != nil {
		b.Fatal(err)
	}
	b.Run("copy", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			outputSink = output
		}
	})
	b.Run("authentication", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			if err := output.ValidateAuthentication(); err != nil {
				b.Fatal(err)
			}
		}
	})
}

var outputSink Output
var stringSink string
