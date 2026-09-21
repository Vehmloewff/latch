// Command gen generates TypeScript/Dart/Go clients for the "basic" example
// protocol. It never uses go:generate — it is a plain program that builds
// the same Server as examples/basic/server and asks it to emit clients.
package main

import (
	"log"

	"github.com/vehmloewff/latch"
	"github.com/vehmloewff/latch/examples/basic/api"
)

func main() {
	lw := api.Build()

	err := lw.GenerateTypeScript(latchwire.TypeScriptOptions{
		OutputDir: "examples/basic/generated/typescript",
	})
	if err == nil {
		err = lw.GenerateDart(latchwire.DartOptions{
			OutputDir: "examples/basic/generated/dart",
			Package:   "basic_client",
		})
	}
	if err == nil {
		err = lw.GenerateGo(latchwire.GoOptions{
			OutputDir: "examples/basic/generated/golang",
			Package:   "basicclient",
		})
	}
	if err != nil {
		log.Fatal(err)
	}

	log.Println("generated clients written to examples/basic/generated")
}
