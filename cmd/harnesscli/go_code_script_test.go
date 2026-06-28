package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGoCodeScriptRoutesDailyCommands(t *testing.T) {
	scriptPath, err := filepath.Abs(filepath.Join("..", "..", "scripts", "go-code.sh"))
	if err != nil {
		t.Fatalf("resolve go-code script path: %v", err)
	}

	binDir := t.TempDir()
	recordFile := filepath.Join(t.TempDir(), "harnesscli.args")
	writeExecutable(t, filepath.Join(binDir, "curl"), "#!/usr/bin/env bash\nexit 0\n")
	writeExecutable(t, filepath.Join(binDir, "harnessd"), "#!/usr/bin/env bash\nexit 0\n")
	writeExecutable(t, filepath.Join(binDir, "harnesscli"), "#!/usr/bin/env bash\nprintf '%s\\n' \"$*\" >> \"$RECORD_FILE\"\n")

	cases := []struct {
		name string
		args []string
		want string
	}{
		{name: "runs", args: []string{"runs"}, want: "list -base-url http://127.0.0.1:19080"},
		{name: "show", args: []string{"show", "run_123"}, want: "status -base-url http://127.0.0.1:19080 run_123"},
		{name: "continue", args: []string{"continue", "run_123", "follow up"}, want: "continue -base-url http://127.0.0.1:19080 run_123 follow up"},
		{name: "search", args: []string{"search", "terminal"}, want: "search -base-url http://127.0.0.1:19080 terminal"},
		{name: "improve", args: []string{"improve", "--dry-run", "--target", "internal/server"}, want: "improve -base-url http://127.0.0.1:19080 --dry-run --target internal/server"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(recordFile, nil, 0o600); err != nil {
				t.Fatalf("reset record file: %v", err)
			}
			cmd := exec.Command("bash", append([]string{scriptPath}, tc.args...)...)
			cmd.Env = append(os.Environ(),
				"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
				"HARNESS_ADDR=:19080",
				"RECORD_FILE="+recordFile,
			)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("go-code %v failed: %v\n%s", tc.args, err, out)
			}
			gotRaw, err := os.ReadFile(recordFile)
			if err != nil {
				t.Fatalf("read record file: %v", err)
			}
			got := strings.TrimSpace(string(gotRaw))
			if got != tc.want {
				t.Fatalf("harnesscli args = %q, want %q\nscript output:\n%s", got, tc.want, out)
			}
		})
	}
}

func writeExecutable(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write executable %s: %v", path, err)
	}
}
