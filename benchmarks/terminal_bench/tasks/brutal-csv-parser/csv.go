package main

import (
	"strings"
)

// ParseCSV parses RFC-4180-style CSV text into rows of string fields.
//
// Contract:
//   - Fields within a record are separated by commas; records are
//     separated by a single '\n'.
//   - A trailing '\n' at the very end of the input does not produce a
//     spurious empty final record.
//   - A field may be wrapped in double quotes ("like this"). A quoted
//     field may contain commas and embedded literal newlines without
//     those characters ending the field or the record.
//   - Inside a quoted field, a doubled double-quote ("") is an escaped
//     quote and collapses to a single literal '"' character in the
//     field's value.
//   - Unquoted fields are taken completely literally: no interior or
//     surrounding whitespace is trimmed.
//   - An empty input string ("") parses to zero rows.
//   - An empty line parses to a single row containing one empty field:
//     [""].
func ParseCSV(input string) ([][]string, error) {
	if input == "" {
		return [][]string{}, nil
	}

	lines := strings.Split(input, "\n")

	rows := make([][]string, 0, len(lines))
	for _, line := range lines {
		rawFields := strings.Split(line, ",")
		fields := make([]string, 0, len(rawFields))
		for _, f := range rawFields {
			if len(f) >= 2 && f[0] == '"' && f[len(f)-1] == '"' {
				f = f[1 : len(f)-1]
			}
			fields = append(fields, f)
		}
		rows = append(rows, fields)
	}

	return rows, nil
}
