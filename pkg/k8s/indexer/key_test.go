package indexer

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEncodeKeyIsUnambiguous(t *testing.T) {
	t.Parallel()

	keys := [][]string{
		{"a/b", "c"},
		{"a", "b/c"},
		{"", "tail"},
		{"0:", "tail"},
		{"領域/一", "雪"},
		{"領域", "一/雪"},
	}

	encoded := make(map[string][]string, len(keys))
	for _, key := range keys {
		value := EncodeKey(key)
		if previous, exists := encoded[value]; exists {
			t.Fatalf("%q and %q encode to the same key %q", previous, key, value)
		}
		encoded[value] = key
	}

	require.Len(t, encoded, len(keys))
}

func TestHasEncodedKeyPrefixMatchesComponents(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		key        []string
		prefix     []string
		wantPrefix bool
	}{
		{name: "complete component", key: []string{"a", "b/c"}, prefix: []string{"a"}, wantPrefix: true},
		{name: "slash is not a boundary", key: []string{"a/b", "c"}, prefix: []string{"a"}, wantPrefix: false},
		{name: "empty component", key: []string{"", "tail"}, prefix: []string{""}, wantPrefix: true},
		{name: "empty component list", key: []string{"a", "tail"}, prefix: nil, wantPrefix: false},
		{name: "unicode component", key: []string{"領域/一", "雪"}, prefix: []string{"領域/一"}, wantPrefix: true},
		{name: "unicode slash is not a boundary", key: []string{"領域/一", "雪"}, prefix: []string{"領域"}, wantPrefix: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.wantPrefix, HasEncodedKeyPrefix(EncodeKey(tt.key), EncodeKey(tt.prefix)))
		})
	}
}
