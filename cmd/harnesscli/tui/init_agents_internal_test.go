package tui

import "testing"

// TestExtractAgentsMarkdown covers the markdown extraction used when the /init
// run completes: the assistant reply becomes the AGENTS.md content, with a
// single wrapping code fence removed when present.
func TestExtractAgentsMarkdown(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "plain markdown passes through",
			input: "# Title\n\nbody\n",
			want:  "# Title\n\nbody",
		},
		{
			name:  "markdown fence unwrapped",
			input: "```markdown\n# Title\n\nbody\n```\n",
			want:  "# Title\n\nbody",
		},
		{
			name:  "bare fence unwrapped",
			input: "```\n# Title\n```\n",
			want:  "# Title",
		},
		{
			name:  "unterminated fence still yields content",
			input: "```markdown\n# Title\n",
			want:  "# Title",
		},
		{
			name:  "leading whitespace before fence",
			input: "  \n```markdown\n# Title\n```\n",
			want:  "# Title",
		},
		{
			name:  "empty input stays empty",
			input: "",
			want:  "",
		},
		{
			name:  "whitespace only stays empty",
			input: "   \n  \n",
			want:  "",
		},
		{
			name:  "fence only yields empty",
			input: "```markdown\n```\n",
			want:  "",
		},
		{
			name:  "inner fences are preserved",
			input: "# Title\n\n```sh\ngo build ./...\n```\n",
			want:  "# Title\n\n```sh\ngo build ./...\n```",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := extractAgentsMarkdown(tc.input); got != tc.want {
				t.Errorf("extractAgentsMarkdown(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}
