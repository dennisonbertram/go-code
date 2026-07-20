package workspace

import "testing"

func TestHetznerSanitizeName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"workspace-run_2c6f85ae-cf0f-42e7-a95b-3a93d11ddae8", "workspace-run-2c6f85ae-cf0f-42e7-a95b-3a93d11ddae8"},
		{"Workspace_RUN_ABC", "workspace-run-abc"},
		{"---weird___", "weird"},
		{"", "workspace"},
	}
	for _, c := range cases {
		got := hetznerSanitizeName(c.in)
		if got != c.want {
			t.Errorf("hetznerSanitizeName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
