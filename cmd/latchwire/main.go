// Command latchwire is a small CLI around Latchwire's code generators.
//
// Reflection requires a running Go program with the actual registered
// types in memory, so this CLI cannot — and does not pretend to — generate
// clients directly from a Go package on disk. The first-class generation
// mechanism is always programmatic: build your Server, then call
// Server.Generate (see examples/basic/gen). This CLI instead consumes a
// manifest file already produced by Server.WriteManifest, letting
// generation run as a separate step (e.g. in CI, or against a manifest
// published by a running service) without needing the server's source
// available at generation time:
//
//	latchwire generate --manifest latchwire.json --typescript ./gen/ts --dart ./gen/dart --go ./gen/go
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/vehmloewff/latchwire/codegen/dart"
	"github.com/vehmloewff/latchwire/codegen/golang"
	"github.com/vehmloewff/latchwire/codegen/typescript"
	"github.com/vehmloewff/latchwire/protocol"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	switch os.Args[1] {
	case "generate":
		if err := runGenerate(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "latchwire generate:", err)
			os.Exit(1)
		}
	case "-h", "--help", "help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "latchwire: unknown command %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `Usage:

  latchwire generate --manifest <path> [--typescript <dir>] [--dart <dir>] [--go <dir>]

The manifest file is produced by calling Server.WriteManifest from your own
Go program (see examples/basic/gen). At least one of --typescript, --dart,
or --go must be given.`)
}

type manifestDoc struct {
	LatchwireVersion int                `json:"latchwireVersion"`
	IR               *protocol.Protocol `json:"ir"`
}

func runGenerate(args []string) error {
	fs := flag.NewFlagSet("generate", flag.ContinueOnError)
	manifestPath := fs.String("manifest", "", "path to a manifest JSON file produced by Server.WriteManifest")
	tsDir := fs.String("typescript", "", "output directory for the generated TypeScript client")
	dartDir := fs.String("dart", "", "output directory for the generated Dart client")
	goDir := fs.String("go", "", "output directory for the generated Go client")
	goPackage := fs.String("go-package", "", "generated Go package name (default derived from the protocol name)")
	dartPackage := fs.String("dart-package", "", "generated Dart pubspec/package name (default derived from the protocol name)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *manifestPath == "" {
		return fmt.Errorf("--manifest is required")
	}
	if *tsDir == "" && *dartDir == "" && *goDir == "" {
		return fmt.Errorf("at least one of --typescript, --dart, or --go is required")
	}

	raw, err := os.ReadFile(*manifestPath)
	if err != nil {
		return fmt.Errorf("read manifest: %w", err)
	}

	var doc manifestDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("parse manifest: %w", err)
	}
	if doc.IR == nil {
		return fmt.Errorf("manifest has no \"ir\" field; it must be produced by Server.WriteManifest (latchwireVersion %d)", doc.LatchwireVersion)
	}

	if *tsDir != "" {
		files, err := typescript.Generate(doc.IR, typescript.Options{})
		if err != nil {
			return fmt.Errorf("generate typescript: %w", err)
		}
		if err := writeFiles(*tsDir, files); err != nil {
			return fmt.Errorf("write typescript output: %w", err)
		}
	}

	if *dartDir != "" {
		files, err := dart.Generate(doc.IR, dart.Options{Package: *dartPackage})
		if err != nil {
			return fmt.Errorf("generate dart: %w", err)
		}
		if err := writeFiles(*dartDir, files); err != nil {
			return fmt.Errorf("write dart output: %w", err)
		}
		dart.FormatDir(*dartDir)
	}

	if *goDir != "" {
		files, err := golang.Generate(doc.IR, golang.Options{Package: *goPackage})
		if err != nil {
			return fmt.Errorf("generate go: %w", err)
		}
		if err := writeFiles(*goDir, files); err != nil {
			return fmt.Errorf("write go output: %w", err)
		}
	}

	return nil
}

func writeFiles(dir string, files map[string][]byte) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for name, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, content, 0o644); err != nil {
			return err
		}
	}
	return nil
}
