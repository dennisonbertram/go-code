package skills

import (
	"reflect"
	"testing"
)

func TestSplitArgs(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{
			name: "acceptance: quoted multi-word token",
			in:   `run "hello world" --fast`,
			want: []string{"run", "hello world", "--fast"},
		},
		{
			name: "empty input",
			in:   ``,
			want: nil,
		},
		{
			name: "whitespace only",
			in:   "  \t \n ",
			want: nil,
		},
		{
			name: "plain tokens split like strings.Fields",
			in:   `alpha beta gamma`,
			want: []string{"alpha", "beta", "gamma"},
		},
		{
			name: "consecutive and surrounding whitespace",
			in:   "  alpha   beta\t\ngamma  ",
			want: []string{"alpha", "beta", "gamma"},
		},
		{
			name: "single quotes group",
			in:   `'hello world' next`,
			want: []string{"hello world", "next"},
		},
		{
			name: "double quotes literal inside single quotes",
			in:   `'say "hi" loudly'`,
			want: []string{`say "hi" loudly`},
		},
		{
			name: "single quotes literal inside double quotes",
			in:   `"it's done"`,
			want: []string{"it's done"},
		},
		{
			name: "mixed quotes in one arg string",
			in:   `deploy "staging eu" 'prod us' fast`,
			want: []string{"deploy", "staging eu", "prod us", "fast"},
		},
		{
			name: "escaped space outside quotes",
			in:   `hello\ world`,
			want: []string{"hello world"},
		},
		{
			name: "escaped quote inside double quotes",
			in:   `"say \"hi\""`,
			want: []string{`say "hi"`},
		},
		{
			name: "escaped quote outside quotes",
			in:   `a\"b`,
			want: []string{`a"b`},
		},
		{
			name: "escaped backslash",
			in:   `a\\b`,
			want: []string{`a\b`},
		},
		{
			name: "escaped single quote inside single quotes is not special",
			in:   `'a\'b`,
			want: []string{`a\b`},
		},
		{
			name: "trailing backslash kept literal",
			in:   `abc\`,
			want: []string{`abc\`},
		},
		{
			name: "unterminated double quote consumes rest of string",
			in:   `run "hello world`,
			want: []string{"run", "hello world"},
		},
		{
			name: "unterminated single quote consumes rest of string",
			in:   `'abc def`,
			want: []string{"abc def"},
		},
		{
			name: "adjacent quoted and unquoted segments join one token",
			in:   `a"b c"d`,
			want: []string{"ab cd"},
		},
		{
			name: "flag with quoted value",
			in:   `--flag="some value"`,
			want: []string{"--flag=some value"},
		},
		{
			name: "adjacent quoted segments join",
			in:   `"a b"'c d'`,
			want: []string{"a bc d"},
		},
		{
			name: "empty double-quoted segment yields empty token",
			in:   `""`,
			want: []string{""},
		},
		{
			name: "empty single-quoted segment yields empty token",
			in:   `''`,
			want: []string{""},
		},
		{
			name: "empty quoted segment adjacent to text",
			in:   `a""b`,
			want: []string{"ab"},
		},
		{
			name: "empty quoted token among others",
			in:   `x "" y`,
			want: []string{"x", "", "y"},
		},
		{
			name: "whitespace inside quotes is literal",
			in:   "\"a\tb\nc\"",
			want: []string{"a\tb\nc"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := SplitArgs(tt.in)
			if err != nil {
				t.Fatalf("SplitArgs(%q) returned unexpected error: %v", tt.in, err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("SplitArgs(%q) = %#v, want %#v", tt.in, got, tt.want)
			}
		})
	}
}
