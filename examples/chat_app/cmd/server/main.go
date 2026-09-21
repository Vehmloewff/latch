// Command server serves the chat app protocol over WebSocket.
package main

import (
	"log"
	"net/http"
	"os"
	"strconv"

	"github.com/vehmloewff/latch/examples/chat_app/api"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	if _, err := strconv.Atoi(port); err != nil {
		log.Fatalf("invalid PORT %q: %v", port, err)
	}
	addr := ":" + port

	lw := api.Build()

	mux := http.NewServeMux()
	mux.Handle("/ws", lw)

	log.Printf("chat app server listening on %s (ws://localhost:%s/ws)", addr, port)
	log.Fatal(http.ListenAndServe(addr, mux))
}
