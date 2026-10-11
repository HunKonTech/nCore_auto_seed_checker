// Command buildwasm compiles the web interface (web/app) to WebAssembly and
// copies the matching wasm_exec.js loader next to it, into web/static.
// It is run by `go generate` from the repository root.
package main

import (
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func main() {
	out := filepath.Join("web", "static")

	build := exec.Command("go", "build", "-trimpath", "-ldflags=-s -w", "-o", filepath.Join(out, "app.wasm"), "./web/app")
	build.Env = append(os.Environ(), "GOOS=js", "GOARCH=wasm")
	build.Stdout, build.Stderr = os.Stdout, os.Stderr
	if err := build.Run(); err != nil {
		log.Fatal("building web/app: ", err)
	}

	goroot, err := exec.Command("go", "env", "GOROOT").Output()
	if err != nil {
		log.Fatal(err)
	}
	root := strings.TrimSpace(string(goroot))
	// wasm_exec.js moved from misc/wasm to lib/wasm in Go 1.24.
	var js []byte
	for _, dir := range []string{"lib", "misc"} {
		if js, err = os.ReadFile(filepath.Join(root, dir, "wasm", "wasm_exec.js")); err == nil {
			break
		}
	}
	if err != nil {
		log.Fatal("wasm_exec.js not found in GOROOT: ", err)
	}
	if err := os.WriteFile(filepath.Join(out, "wasm_exec.js"), js, 0644); err != nil {
		log.Fatal(err)
	}
}
