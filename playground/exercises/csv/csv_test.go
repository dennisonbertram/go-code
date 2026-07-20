package main

import (
	"reflect"
	"testing"
)

func TestParseCSV(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []map[string]string
	}{
		{
			name:  "empty input",
			input: "",
			want:  nil,
		},
		{
			name:  "single row",
			input: "a,b,c\n1,2,3",
			want:  []map[string]string{{"a": "1", "b": "2", "c": "3"}},
		},
		{
			name:  "multi row with trailing newline",
			input: "foo,bar\nhello,world\ngopher,go\n",
			want: []map[string]string{
				{"foo": "hello", "bar": "world"},
				{"foo": "gopher", "bar": "go"},
			},
		},
		{
			name:  "header only with trailing newline",
			input: "a,b,c\n",
			want:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseCSV(tt.input)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ParseCSV(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}
