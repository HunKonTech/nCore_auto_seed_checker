// Package web holds the web interface: a Go WebAssembly app (web/app) and the
// static files that load it. Run `go generate` in the repository root to
// build web/static/app.wasm before building the server.
package web

import "embed"

//go:embed static
var Static embed.FS
