package main

import (
	"io/fs"
	"net/http"

	webembed "nms"
)

// WebFiles exposes the embedded dev dashboard as an http.FileSystem so it can
// be served same-origin from the binary. The embedded data lives in the
// top-level web/ directory via the module-root webembed package: //go:embed
// is package-relative and cannot reach ../web from here.
func WebFiles() http.FileSystem {
	sub, err := fs.Sub(webembed.WebFS, "web")
	if err != nil {
		// "web" is embedded at build time; a missing subdirectory is a
		// build-time error, not a runtime condition.
		panic(err)
	}
	return http.FS(sub)
}
