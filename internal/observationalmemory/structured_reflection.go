package observationalmemory

import (
	"strconv"
	"strings"
)

// StructuredReflection holds the parsed output of a structured reflection prompt.
// SchemaVersion==1 means the reflection was produced with the structured prompt
// (SUMMARY / SUPERSESSIONS / CONTRADICTIONS sections). SchemaVersion==0 means
// legacy plain-text — the entire raw string is stored in Summary.
type StructuredReflection struct {
	Summary        string          `json:"summary"`
	Supersessions  []Supersession  `json:"supersessions,omitempty"`
	Contradictions []Contradiction `json:"contradictions,omitempty"`
	SchemaVersion  int             `json:"schema_version"`
}

// Supersession records that a newer observation replaces an older one.
type Supersession struct {
	NewerSeq int64  `json:"newer_seq"`
	OlderSeq int64  `json:"older_seq"`
	Reason   string `json:"reason"`
}

// Contradiction records two observations that are in conflict.
type Contradiction struct {
	SeqA   int64  `json:"seq_a"`
	SeqB   int64  `json:"seq_b"`
	Detail string `json:"detail"`
}

// ParseStructuredReflection parses the raw LLM output from a structured
// reflection prompt. If the output contains a "SUMMARY:" header it is treated
// as SchemaVersion=1 and all three sections are parsed. Otherwise the entire
// text is treated as the Summary with SchemaVersion=0 (legacy plain text).
func ParseStructuredReflection(raw string) StructuredReflection {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return StructuredReflection{SchemaVersion: 0}
	}

	// Detect structured format by looking for a SUMMARY: header.
	if !strings.Contains(raw, "SUMMARY:") {
		// Legacy plain-text reflection.
		return StructuredReflection{
			Summary:       raw,
			SchemaVersion: 0,
		}
	}

	sr := StructuredReflection{SchemaVersion: 1}

	// Split into labelled sections. We recognise the three section headers.
	// Everything between SUMMARY: and the next header (or end) is the summary.
	// SUPERSESSIONS: lines follow the format:
	//   - [seq:N] replaces [seq:M]: reason text
	// CONTRADICTIONS: lines follow the format:
	//   - [seq:N] vs [seq:M]: detail text

	type section struct {
		name    string
		content strings.Builder
	}

	sections := []string{"SUMMARY:", "SUPERSESSIONS:", "CONTRADICTIONS:"}
	sectionContent := map[string]*strings.Builder{}
	for _, s := range sections {
		var b strings.Builder
		sectionContent[s] = &b
	}

	currentSection := ""
	for _, line := range strings.Split(raw, "\n") {
		trimmed := strings.TrimSpace(line)
		matched := false
		for _, sec := range sections {
			if strings.HasPrefix(trimmed, sec) {
				currentSection = sec
				// Capture anything on the same line after the header.
				remainder := strings.TrimSpace(strings.TrimPrefix(trimmed, sec))
				if remainder != "" {
					sectionContent[sec].WriteString(remainder)
					sectionContent[sec].WriteByte('\n')
				}
				matched = true
				break
			}
		}
		if matched {
			continue
		}
		if currentSection != "" {
			sectionContent[currentSection].WriteString(line)
			sectionContent[currentSection].WriteByte('\n')
		}
	}

	sr.Summary = strings.TrimSpace(sectionContent["SUMMARY:"].String())
	sr.Supersessions = parseSupersessions(sectionContent["SUPERSESSIONS:"].String())
	sr.Contradictions = parseContradictions(sectionContent["CONTRADICTIONS:"].String())

	return sr
}

// parseSupersessions parses lines of the form:
//
//   - [seq:N] replaces [seq:M]: reason
func parseSupersessions(block string) []Supersession {
	var out []Supersession
	for _, line := range strings.Split(block, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Remove leading "- " bullet if present.
		line = strings.TrimPrefix(line, "- ")
		line = strings.TrimSpace(line)

		// Expected: [seq:N] replaces [seq:M]: reason
		newerSeq, rest, ok := extractSeqBracket(line)
		if !ok {
			continue
		}
		rest = strings.TrimSpace(rest)
		// Expect "replaces"
		if !strings.HasPrefix(strings.ToLower(rest), "replaces") {
			continue
		}
		rest = strings.TrimSpace(rest[len("replaces"):])
		olderSeq, rest, ok := extractSeqBracket(rest)
		if !ok {
			continue
		}
		rest = strings.TrimSpace(rest)
		// Strip optional leading colon.
		rest = strings.TrimPrefix(rest, ":")
		reason := strings.TrimSpace(rest)
		out = append(out, Supersession{
			NewerSeq: newerSeq,
			OlderSeq: olderSeq,
			Reason:   reason,
		})
	}
	return out
}

// parseContradictions parses lines of the form:
//
//   - [seq:N] vs [seq:M]: detail
func parseContradictions(block string) []Contradiction {
	var out []Contradiction
	for _, line := range strings.Split(block, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Remove leading "- " bullet if present.
		line = strings.TrimPrefix(line, "- ")
		line = strings.TrimSpace(line)

		// Expected: [seq:N] vs [seq:M]: detail
		seqA, rest, ok := extractSeqBracket(line)
		if !ok {
			continue
		}
		rest = strings.TrimSpace(rest)
		// Expect "vs"
		lower := strings.ToLower(rest)
		if !strings.HasPrefix(lower, "vs") {
			continue
		}
		rest = strings.TrimSpace(rest[len("vs"):])
		seqB, rest, ok := extractSeqBracket(rest)
		if !ok {
			continue
		}
		rest = strings.TrimSpace(rest)
		// Strip optional leading colon.
		rest = strings.TrimPrefix(rest, ":")
		detail := strings.TrimSpace(rest)
		out = append(out, Contradiction{
			SeqA:   seqA,
			SeqB:   seqB,
			Detail: detail,
		})
	}
	return out
}

// extractSeqBracket extracts [seq:N] from the start of s, returning the
// parsed integer, the remaining string after the bracket, and whether it
// succeeded.
func extractSeqBracket(s string) (int64, string, bool) {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "[seq:") {
		return 0, s, false
	}
	end := strings.IndexByte(s, ']')
	if end == -1 {
		return 0, s, false
	}
	inner := s[len("[seq:"):end]
	n, err := strconv.ParseInt(strings.TrimSpace(inner), 10, 64)
	if err != nil {
		return 0, s, false
	}
	return n, s[end+1:], true
}
