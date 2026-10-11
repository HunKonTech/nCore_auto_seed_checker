package main

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"

	"ncore_automation/web"
)

func startWebServer(port int, hub *Hub) {
	addr := fmt.Sprintf(":%d", port)
	log.Printf("Starting web server on %s...", addr)

	static, err := fs.Sub(web.Static, "static")
	if err != nil {
		log.Fatal(err)
	}
	if _, err := fs.Stat(static, "app.wasm"); err != nil {
		log.Println("WARNING: web/static/app.wasm is missing, the web interface will not load. Run `go generate` before `go build`.")
	}

	mux := http.NewServeMux()
	mux.Handle("/", http.FileServerFS(static))
	mux.Handle("/ws", hub.Handler())
	mux.HandleFunc("/api/history", handleHistory)

	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal("Server failed:", err)
	}
}

func handleHistory(w http.ResponseWriter, r *http.Request) {
	hm, err := NewHistoryManager()
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to load history: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(hm.GetRecords())
}
