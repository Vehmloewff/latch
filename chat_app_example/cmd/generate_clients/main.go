// Command generate_clients generates all clients for the chat app example. The checked-in
// integration programs live beside the generated clients and are not overwritten.
package main

import (
	"log"

	"path/filepath"

	"github.com/vehmloewff/latch"
	"github.com/vehmloewff/latch/chat_app_example/api"
)

func main() {
	root := "."
	lw := api.Build()
	if err := lw.GenerateTypeScript(latch.TypeScriptOptions{OutputDir: filepath.Join(root, "typescript")}); err != nil {
		log.Fatal(err)
	}
	if err := lw.GenerateDart(latch.DartOptions{OutputDir: filepath.Join(root, "dart"), Package: "chat_app_client"}); err != nil {
		log.Fatal(err)
	}
	if err := lw.GenerateGo(latch.GoOptions{OutputDir: filepath.Join(root, "golang"), Package: "chatappclient"}); err != nil {
		log.Fatal(err)
	}

	log.Println("generated clients written to chat_app_example/{typescript,dart,golang}")
}
