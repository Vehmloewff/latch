// Command generate_clients generates all clients for the chat app example. The checked-in
// integration programs live beside the generated clients and are not overwritten.
package main

import (
	"log"
	"os"

	"path/filepath"

	"github.com/vehmloewff/latch"
	"github.com/vehmloewff/latch/chat_app_example/api"
)

func main() {
	root := "."
	lw := api.Build()
	selected := func(language string) bool {
		if len(os.Args) == 1 {
			return true
		}
		for _, arg := range os.Args[1:] {
			if arg == language {
				return true
			}
		}
		return false
	}
	if selected("typescript") {
		if err := lw.GenerateTypeScript(latch.TypeScriptOptions{OutputDir: filepath.Join(root, "typescript")}); err != nil {
			log.Fatal(err)
		}
	}
	if selected("dart") {
		if err := lw.GenerateDart(latch.DartOptions{OutputDir: filepath.Join(root, "dart"), Package: "chat_app_client"}); err != nil {
			log.Fatal(err)
		}
	}
	if selected("go") {
		if err := lw.GenerateGo(latch.GoOptions{OutputDir: filepath.Join(root, "golang"), Package: "chatappclient"}); err != nil {
			log.Fatal(err)
		}
	}
	if selected("swift") {
		if err := lw.GenerateSwift(latch.SwiftOptions{OutputDir: filepath.Join(root, "swift", "Sources", "LatchClient")}); err != nil {
			log.Fatal(err)
		}
	}
	if selected("kotlin") {
		if err := lw.GenerateKotlin(latch.KotlinOptions{OutputDir: filepath.Join(root, "kotlin")}); err != nil {
			log.Fatal(err)
		}
	}
	log.Println("generated requested clients")
}
