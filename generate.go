package main

// Builds the WebAssembly web interface into web/static, which is embedded
// into the binary. Run `go generate` before `go build`.
//go:generate go run ./tools/buildwasm
