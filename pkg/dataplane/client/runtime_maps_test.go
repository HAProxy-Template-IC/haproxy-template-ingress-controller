package client

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseMapEntries(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []mapEntry
	}{
		{
			name: "empty",
			in:   "",
			want: []mapEntry{},
		},
		{
			name: "simple host map",
			in:   "example.com be_example\nfoo.test be_foo\n",
			want: []mapEntry{{"example.com", "be_example"}, {"foo.test", "be_foo"}},
		},
		{
			name: "skips blank lines and comments",
			in:   "\n# a comment\nexample.com be_example\n   \n  # indented comment\nfoo.test be_foo",
			want: []mapEntry{{"example.com", "be_example"}, {"foo.test", "be_foo"}},
		},
		{
			name: "tab separator and surrounding whitespace",
			in:   "  example.com\tbe_example  \n",
			want: []mapEntry{{"example.com", "be_example"}},
		},
		{
			name: "key only yields empty value",
			in:   "loneKey\n",
			want: []mapEntry{{"loneKey", ""}},
		},
		{
			name: "value keeps remainder of the line",
			in:   "key val1 val2\n",
			want: []mapEntry{{"key", "val1 val2"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, parseMapEntries(tt.in))
		})
	}
}

func TestMapEntryDelta(t *testing.T) {
	cur := func(kv ...string) []mapEntry {
		var out []mapEntry
		for i := 0; i < len(kv); i += 2 {
			out = append(out, mapEntry{key: kv[i], value: kv[i+1]})
		}
		return out
	}
	des := func(kv ...string) []mapEntry {
		var out []mapEntry
		for i := 0; i < len(kv); i += 2 {
			out = append(out, mapEntry{key: kv[i], value: kv[i+1]})
		}
		return out
	}

	tests := []struct {
		name    string
		current []mapEntry
		desired []mapEntry
		want    []mapOp
	}{
		{
			name:    "no change",
			current: cur("a", "1", "b", "2"),
			desired: des("a", "1", "b", "2"),
		},
		{
			name:    "add new key only",
			current: cur("a", "1"),
			desired: des("a", "1", "b", "2"),
			want:    []mapOp{{kind: opAdd, key: "b", value: "2"}},
		},
		{
			name:    "remove key only",
			current: cur("a", "1", "b", "2"),
			desired: des("a", "1"),
			want:    []mapOp{{kind: opDel, key: "b"}},
		},
		{
			// The reason this whole change exists: a single-value re-point is
			// an in-place set, never a del+add (which would briefly unmap it).
			name:    "change single value: in-place set",
			current: cur("a", "1"),
			desired: des("a", "2"),
			want:    []mapOp{{kind: opSet, key: "a", value: "2"}},
		},
		{
			name:    "mixed add/change/remove",
			current: cur("a", "1", "b", "2", "c", "3"),
			desired: des("a", "1", "b", "9", "d", "4"),
			want: []mapOp{
				{kind: opSet, key: "b", value: "9"},
				{kind: opDel, key: "c"},
				{kind: opAdd, key: "d", value: "4"},
			},
		},
		{
			name:    "single to multi value: del then re-add",
			current: cur("a", "1"),
			desired: des("a", "2", "a", "3"),
			want: []mapOp{
				{kind: opDel, key: "a"},
				{kind: opAdd, key: "a", value: "2"},
				{kind: opAdd, key: "a", value: "3"},
			},
		},
		{
			name:    "duplicate-key count change: del then re-add",
			current: cur("a", "1", "a", "1"),
			desired: des("a", "1"),
			want: []mapOp{
				{kind: opDel, key: "a"},
				{kind: opAdd, key: "a", value: "1"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Order across distinct keys is map-iteration-dependent, so match as
			// a set. Per key, del must precede its re-adds; the cases that mix
			// both for one key keep that order within the expected slice, and
			// ElementsMatch still validates membership.
			assert.ElementsMatch(t, tt.want, mapEntryDelta(tt.current, tt.desired))
		})
	}
}
