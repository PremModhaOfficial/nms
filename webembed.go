// Package webembed embeds the top-level web/ directory (the dev dashboard
// frontend, owned by the frontend worker) into the binary. //go:embed paths
// are relative to the package directory and cannot traverse upward with "..",
// so the embed directive must live at the module root where web/ is a direct
// child. cmd/app/embed.go exposes WebFiles() on top of this.
package webembed

import "embed"

//go:embed all:web
var WebFS embed.FS
