package indexer

import (
	"testing"
)

// TestNewFieldSelectorMatcher_ValidExpressions verifies parsing of valid
// expressions. Parsing correctness (field path + expected value split,
// whitespace trimming, split-on-first-'=') is observed end-to-end through
// Matches against a resource whose field holds wantValue, plus the
// Expression() round-trip.
func TestNewFieldSelectorMatcher_ValidExpressions(t *testing.T) {
	tests := []struct {
		name          string
		expression    string
		wantFieldPath string
		wantValue     string
	}{
		{
			name:          "simple field",
			expression:    "spec.ingressClassName=haproxy-internal",
			wantFieldPath: "spec.ingressClassName",
			wantValue:     "haproxy-internal",
		},
		{
			name:          "metadata field",
			expression:    "metadata.namespace=production",
			wantFieldPath: "metadata.namespace",
			wantValue:     "production",
		},
		{
			name:          "empty value",
			expression:    "spec.field=",
			wantFieldPath: "spec.field",
			wantValue:     "",
		},
		{
			name:          "value with equals sign",
			expression:    "metadata.annotations['key']=value=with=equals",
			wantFieldPath: "metadata.annotations['key']",
			wantValue:     "value=with=equals",
		},
		{
			name:          "whitespace trimmed",
			expression:    " spec.field = value ",
			wantFieldPath: "spec.field",
			wantValue:     "value",
		},
		{
			name:          "label selector style",
			expression:    "metadata.labels['app']=nginx",
			wantFieldPath: "metadata.labels['app']",
			wantValue:     "nginx",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matcher, err := NewFieldSelectorMatcher(tt.expression)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			// Expression() must round-trip the original input verbatim.
			if matcher.Expression() != tt.expression {
				t.Errorf("Expression() = %q, want %q", matcher.Expression(), tt.expression)
			}

			// Build a resource whose wantFieldPath holds wantValue; a
			// correctly parsed matcher must accept it. This exercises both
			// the parsed field path and expected value.
			resource := resourceWithField(tt.wantFieldPath, tt.wantValue)
			matches, err := matcher.Matches(resource)
			if err != nil {
				t.Fatalf("Matches returned unexpected error: %v", err)
			}
			if !matches {
				t.Errorf("expected matcher %q to accept resource %v with %q=%q",
					tt.expression, resource, tt.wantFieldPath, tt.wantValue)
			}
		})
	}
}

// resourceWithField builds a nested map[string]any resource where the JSONPath
// fieldPath resolves to value. Supports dotted segments and bracketed map keys
// (e.g. metadata.labels['app']).
func resourceWithField(fieldPath, value string) map[string]any {
	segments := parseJSONPathPattern(fieldPath)
	root := map[string]any{}
	cur := root
	for i, seg := range segments {
		if i == len(segments)-1 {
			cur[seg] = value
			break
		}
		next := map[string]any{}
		cur[seg] = next
		cur = next
	}
	return root
}

// TestNewFieldSelectorMatcher_InvalidExpressions verifies error handling for invalid expressions.
func TestNewFieldSelectorMatcher_InvalidExpressions(t *testing.T) {
	tests := []struct {
		name       string
		expression string
		wantErr    string
	}{
		{
			name:       "empty expression",
			expression: "",
			wantErr:    "empty field selector expression",
		},
		{
			name:       "no equals sign",
			expression: "spec.ingressClassName",
			wantErr:    "invalid field selector format",
		},
		{
			name:       "empty field path",
			expression: "=value",
			wantErr:    "empty field path",
		},
		{
			name:       "only whitespace path",
			expression: "   =value",
			wantErr:    "empty field path",
		},
		{
			name:       "invalid jsonpath",
			expression: "metadata.[invalid=value",
			wantErr:    "invalid field selector path",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewFieldSelectorMatcher(tt.expression)
			if err == nil {
				t.Fatal("expected error, got nil")
			}

			if !contains(err.Error(), tt.wantErr) {
				t.Errorf("error %q should contain %q", err.Error(), tt.wantErr)
			}
		})
	}
}

// TestFieldSelectorMatcher_Matches verifies matching behavior.
func TestFieldSelectorMatcher_Matches(t *testing.T) {
	tests := []struct {
		name       string
		expression string
		resource   map[string]any
		wantMatch  bool
	}{
		{
			name:       "matching field value",
			expression: "spec.ingressClassName=haproxy-internal",
			resource: map[string]any{
				"spec": map[string]any{
					"ingressClassName": "haproxy-internal",
				},
			},
			wantMatch: true,
		},
		{
			name:       "non-matching field value",
			expression: "spec.ingressClassName=haproxy-internal",
			resource: map[string]any{
				"spec": map[string]any{
					"ingressClassName": "haproxy-public",
				},
			},
			wantMatch: false,
		},
		{
			name:       "missing field",
			expression: "spec.ingressClassName=haproxy-internal",
			resource: map[string]any{
				"spec": map[string]any{
					"otherField": "value",
				},
			},
			wantMatch: false,
		},
		{
			name:       "missing parent field",
			expression: "spec.ingressClassName=haproxy-internal",
			resource: map[string]any{
				"metadata": map[string]any{
					"name": "test",
				},
			},
			wantMatch: false,
		},
		{
			name:       "empty resource",
			expression: "spec.ingressClassName=haproxy-internal",
			resource:   map[string]any{},
			wantMatch:  false,
		},
		{
			name:       "matching empty value",
			expression: "spec.field=",
			resource: map[string]any{
				"spec": map[string]any{
					"field": "",
				},
			},
			wantMatch: true,
		},
		{
			name:       "matching label",
			expression: "metadata.labels['app']=nginx",
			resource: map[string]any{
				"metadata": map[string]any{
					"labels": map[string]any{
						"app": "nginx",
					},
				},
			},
			wantMatch: true,
		},
		{
			name:       "non-matching label",
			expression: "metadata.labels['app']=nginx",
			resource: map[string]any{
				"metadata": map[string]any{
					"labels": map[string]any{
						"app": "apache",
					},
				},
			},
			wantMatch: false,
		},
		{
			name:       "missing label",
			expression: "metadata.labels['app']=nginx",
			resource: map[string]any{
				"metadata": map[string]any{
					"labels": map[string]any{
						"other": "value",
					},
				},
			},
			wantMatch: false,
		},
		{
			name:       "integer value matching",
			expression: "spec.replicas=3",
			resource: map[string]any{
				"spec": map[string]any{
					"replicas": 3,
				},
			},
			wantMatch: true,
		},
		{
			name:       "boolean value matching",
			expression: "spec.enabled=true",
			resource: map[string]any{
				"spec": map[string]any{
					"enabled": true,
				},
			},
			wantMatch: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matcher, err := NewFieldSelectorMatcher(tt.expression)
			if err != nil {
				t.Fatalf("creating matcher: %v", err)
			}

			matches, err := matcher.Matches(tt.resource)
			if err != nil {
				t.Fatalf("Matches() returned error: %v", err)
			}

			if matches != tt.wantMatch {
				t.Errorf("Matches() = %v, want %v", matches, tt.wantMatch)
			}
		})
	}
}

// TestFieldSelectorMatcher_MatchesIngress verifies matching against Ingress-like resources.
func TestFieldSelectorMatcher_MatchesIngress(t *testing.T) {
	matcher, err := NewFieldSelectorMatcher("spec.ingressClassName=haproxy-internal")
	if err != nil {
		t.Fatalf("creating matcher: %v", err)
	}

	// Ingress with matching class
	internalIngress := map[string]any{
		"apiVersion": "networking.k8s.io/v1",
		"kind":       "Ingress",
		"metadata": map[string]any{
			"name":      "internal-app",
			"namespace": "default",
		},
		"spec": map[string]any{
			"ingressClassName": "haproxy-internal",
			"rules": []any{
				map[string]any{
					"host": "internal.example.com",
				},
			},
		},
	}

	matches, err := matcher.Matches(internalIngress)
	if err != nil {
		t.Fatalf("Matches() returned error: %v", err)
	}
	if !matches {
		t.Error("expected internal ingress to match")
	}

	// Ingress with different class
	publicIngress := map[string]any{
		"apiVersion": "networking.k8s.io/v1",
		"kind":       "Ingress",
		"metadata": map[string]any{
			"name":      "public-app",
			"namespace": "default",
		},
		"spec": map[string]any{
			"ingressClassName": "haproxy-public",
			"rules": []any{
				map[string]any{
					"host": "public.example.com",
				},
			},
		},
	}

	matches, err = matcher.Matches(publicIngress)
	if err != nil {
		t.Fatalf("Matches() returned error: %v", err)
	}
	if matches {
		t.Error("expected public ingress to NOT match")
	}

	// Ingress without ingressClassName
	legacyIngress := map[string]any{
		"apiVersion": "networking.k8s.io/v1",
		"kind":       "Ingress",
		"metadata": map[string]any{
			"name":      "legacy-app",
			"namespace": "default",
		},
		"spec": map[string]any{
			"rules": []any{
				map[string]any{
					"host": "legacy.example.com",
				},
			},
		},
	}

	matches, err = matcher.Matches(legacyIngress)
	if err != nil {
		t.Fatalf("Matches() returned error: %v", err)
	}
	if matches {
		t.Error("expected legacy ingress without ingressClassName to NOT match")
	}
}
