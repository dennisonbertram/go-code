package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// executeAddDirCommand implements /add-dir: it attaches an extra directory to
// the session so runs may read/work in it (harness.RunRequest.ExtraDirs).
//
//	/add-dir                — list the attached directories
//	/add-dir <path>         — attach a directory (relative paths resolve
//	                          against the session workspace)
//	/add-dir remove <path>  — detach a directory ("remove" is a subcommand
//	                          only when followed by a path, so a directory
//	                          literally named "remove" can still be added)
//
// The list is session-scoped (not persisted) and applies to file-tool
// confinement only; the bash sandbox and glob still confine to the primary
// workspace root.
func executeAddDirCommand(m *Model, cmd Command) ([]tea.Cmd, bool) {
	// /add-dir remove <path>
	if len(cmd.Args) >= 2 && cmd.Args[0] == "remove" {
		return m.removeExtraDir(strings.Join(cmd.Args[1:], " ")), false
	}

	// /add-dir (list)
	if len(cmd.Args) == 0 {
		if len(m.extraDirs) == 0 {
			return []tea.Cmd{m.setStatusMsg("No extra directories — use /add-dir <path> to add one")}, false
		}
		return []tea.Cmd{m.setStatusMsg(fmt.Sprintf("Extra directories (%d): %s", len(m.extraDirs), strings.Join(m.extraDirs, ", ")))}, false
	}

	// /add-dir <path> (add; a sole "remove" arg is a literal relative path,
	// see the collision rule above).
	return m.addExtraDir(strings.Join(cmd.Args, " ")), false
}

// addExtraDir validates and attaches dir, returning the status-message command.
func (m *Model) addExtraDir(dir string) []tea.Cmd {
	abs, err := m.resolveAddDirPath(dir)
	if err != nil {
		return []tea.Cmd{m.setStatusMsg(err.Error())}
	}
	for _, existing := range m.extraDirs {
		if existing == abs {
			return []tea.Cmd{m.setStatusMsg("Already added " + abs)}
		}
	}
	m.extraDirs = append(m.extraDirs, abs)
	return []tea.Cmd{m.setStatusMsg("Added " + abs)}
}

// removeExtraDir detaches dir, returning the status-message command.
func (m *Model) removeExtraDir(dir string) []tea.Cmd {
	abs, err := m.resolveAddDirPath(dir)
	if err != nil {
		return []tea.Cmd{m.setStatusMsg(err.Error())}
	}
	for i, existing := range m.extraDirs {
		if existing == abs {
			m.extraDirs = append(m.extraDirs[:i], m.extraDirs[i+1:]...)
			return []tea.Cmd{m.setStatusMsg("Removed " + abs)}
		}
	}
	return []tea.Cmd{m.setStatusMsg(abs + " is not in the extra directories list")}
}

// resolveAddDirPath resolves dir to a canonical absolute path (relative paths
// resolve against the session workspace, falling back to the process working
// directory) and verifies it is an existing directory.
func (m Model) resolveAddDirPath(dir string) (string, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return "", fmt.Errorf("Usage: /add-dir <path>")
	}
	p := dir
	if !filepath.IsAbs(p) {
		base := strings.TrimSpace(m.config.Workspace)
		if base == "" {
			if cwd, err := os.Getwd(); err == nil {
				base = cwd
			}
		}
		p = filepath.Join(base, p)
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", fmt.Errorf("/add-dir: resolve %q: %v", dir, err)
	}
	abs = filepath.Clean(abs)
	fi, err := os.Stat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("/add-dir: %s does not exist", abs)
		}
		return "", fmt.Errorf("/add-dir: %s is not accessible: %v", abs, err)
	}
	if !fi.IsDir() {
		return "", fmt.Errorf("/add-dir: %s is not a directory", abs)
	}
	return abs, nil
}
