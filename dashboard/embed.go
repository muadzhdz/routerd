package dashboard

import (
	"embed"
	"io/fs"
)

//go:embed static
var embeddedStatic embed.FS

// staticFiles is a sub-filesystem rooted at "static/" so that
// http.FileServer serves files at "/" rather than "/static/".
var staticFiles, _ = fs.Sub(embeddedStatic, "static")
