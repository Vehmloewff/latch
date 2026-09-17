// Command server serves the "basic" example protocol over WebSocket.
package main

import (
	"log"
	"net/http"
	"os"

	"github.com/vehmloewff/latchwire/examples/basic/api"
)

func main() {
	addr := os.Getenv("LATCHWIRE_EXAMPLE_ADDR")
	if addr == "" {
		addr = ":8080"
	}

	lw := api.Build()

	mux := http.NewServeMux()
	mux.Handle("/ws", lw)

	log.Printf("basic example server listening on %s (ws://localhost%s/ws)", addr, addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}
