// Package viz serves the embedded session visualizer: a read-only,
// browser-based inspector for harnessd runs (epic #812).
//
// The visualizer is a static shell (plain HTML/JS/CSS, no build step, no
// external assets) embedded into the harnessd binary via go:embed,
// following the precedent in internal/harness/tools/descriptions. It is
// mounted under /viz by internal/server and inherits the standard Bearer
// auth + runs:read scope enforcement applied to every other read route —
// the package itself performs no authentication.
package viz

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed static
var staticFS embed.FS

// Handler returns an http.Handler that serves the embedded visualizer
// assets. It must be mounted on the "/viz/" subtree pattern; the returned
// handler strips the "/viz/" prefix before delegating to a file server
// over the embedded filesystem. Directory requests serve index.html.
func Handler() http.Handler {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		// Unreachable: the go:embed directive above guarantees "static"
		// exists at build time.
		panic("viz: embedded static filesystem unavailable: " + err.Error())
	}
	return http.StripPrefix("/viz/", http.FileServer(http.FS(sub)))
}
