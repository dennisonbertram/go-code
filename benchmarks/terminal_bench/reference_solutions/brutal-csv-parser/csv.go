package main

import (
	"fmt"
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
//
// Reference solution: hand-written state machine (no encoding/csv) that
// tracks whether it is currently inside a quoted field so that commas,
// newlines, and quote characters are only treated as delimiters when they
// are NOT protected by an open quote.
func ParseCSV(input string) ([][]string, error) {
	if input == "" {
		return [][]string{}, nil
	}

	var rows [][]string
	var fields []string
	var field strings.Builder
	inQuotes := false

	n := len(input)
	for i := 0; i < n; i++ {
		c := input[i]

		if inQuotes {
			if c == '"' {
				if i+1 < n && input[i+1] == '"' {
					// Escaped quote: "" -> literal ".
					field.WriteByte('"')
					i++
					continue
				}
				// Closing quote.
				inQuotes = false
				continue
			}
			// Any other byte (including commas and newlines) inside a
			// quoted field is part of the field's literal value.
			field.WriteByte(c)
			continue
		}

		switch c {
		case '"':
			inQuotes = true
		case ',':
			fields = append(fields, field.String())
			field.Reset()
		case '\n':
			fields = append(fields, field.String())
			field.Reset()
			rows = append(rows, fields)
			fields = nil
		default:
			field.WriteByte(c)
		}
	}

	if inQuotes {
		return nil, fmt.Errorf("csv: unterminated quoted field")
	}

	// A trailing newline already closed the final record above; do not
	// append a spurious empty final record for it.
	if input[n-1] == '\n' {
		return rows, nil
	}

	fields = append(fields, field.String())
	rows = append(rows, fields)
	return rows, nil
}
