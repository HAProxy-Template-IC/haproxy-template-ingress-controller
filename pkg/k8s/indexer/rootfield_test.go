package indexer

import "testing"

func TestRootField(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		want    string
	}{
		{name: "simple two-segment", pattern: "metadata.name", want: "metadata"},
		{name: "namespace", pattern: "metadata.namespace", want: "metadata"},
		{name: "bracketed label", pattern: "metadata.labels['kubernetes.io/service-name']", want: "metadata"},
		{name: "spec field", pattern: "spec.ingressClassName", want: "spec"},
		{name: "indexed list", pattern: "spec.rules[0].host", want: "spec"},
		{name: "bare root", pattern: "status", want: "status"},
		{name: "leading dot", pattern: ".metadata.name", want: "metadata"},
		{name: "bracket-first root", pattern: "metadata['name']", want: "metadata"},
		{name: "empty", pattern: "", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := RootField(tt.pattern); got != tt.want {
				t.Errorf("RootField(%q) = %q, want %q", tt.pattern, got, tt.want)
			}
		})
	}
}
