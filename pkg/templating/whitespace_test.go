package templating

import (
	"context"
	"fmt"
	"testing"
)

func TestScriggoWhitespaceTrim(t *testing.T) {
	tests := []struct {
		name     string
		template string
	}{
		{"standard", `{% var x = 1 %}X={{ x }}`},
		{"trim_left", `{%- var x = 1 %}X={{ x }}`},
		{"trim_right", `{% var x = 1 -%}X={{ x }}`},
		{"trim_both", `{%- var x = 1 -%}X={{ x }}`},
		{"short_decl", `{% x := 1 %}X={{ x }}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			engine, err := New(map[string]string{"test": tt.template}, &Options{EntryPoints: []string{"test"}})
			if err != nil {
				fmt.Printf("%s: Compile error: %v\n", tt.name, err)
				return
			}

			output, err := engine.Render(context.Background(), "test", nil)
			if err != nil {
				fmt.Printf("%s: Render error: %v\n", tt.name, err)
				return
			}

			fmt.Printf("%s: Success - Output: %q\n", tt.name, output)
		})
	}
}
