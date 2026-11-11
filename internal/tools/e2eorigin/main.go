// Command e2eorigin is a tiny static file server used only by the local
// end-to-end test harness. It serves .run/www so the tunnel has a stable,
// long-running local target that survives shell restarts.
package main

import (
	"log"
	"net/http"
	"os"
)

func main() {
	addr := os.Getenv("ORIGIN_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8090"
	}
	dir := os.Getenv("ORIGIN_DIR")
	if dir == "" {
		dir = ".run/www"
	}
	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.Dir(dir)))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("ok"))
	})
	log.Printf("e2e origin listening on http://%s serving %s", addr, dir)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}
