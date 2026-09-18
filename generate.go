package latchwire

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/vehmloewff/latchwire/codegen/dart"
	"github.com/vehmloewff/latchwire/codegen/golang"
	"github.com/vehmloewff/latchwire/codegen/typescript"
)

// TypeScriptOptions configures TypeScript client generation.
type TypeScriptOptions struct {
	// OutputDir is the directory generated TypeScript files are written
	// to, created if it does not already exist.
	OutputDir string

	// ClientName overrides the generated class name (default
	// LatchwireClient). See typescript.Options.ClientName.
	ClientName string
}

// DartOptions configures Dart client generation.
type DartOptions struct {
	// OutputDir is the directory the generated Dart package is written to
	// (pubspec.yaml, lib/...), created if it does not already exist.
	OutputDir string

	// Package overrides the generated pubspec/barrel-file package name
	// (default latchwire_client). See dart.Options.Package.
	Package string

	// ClientName overrides the generated class name (default
	// LatchwireClient). See dart.Options.ClientName.
	ClientName string
}

// GoOptions configures Go client generation.
type GoOptions struct {
	// OutputDir is the directory generated Go files are written to
	// (types.go, client.go), created if it does not already exist. It is
	// typically a subdirectory of the consuming Go module, since generated
	// output is plain source files, not a separate module.
	OutputDir string

	// Package overrides the generated package name (default
	// latchwireclient). See golang.Options.Package.
	Package string

	// ClientName overrides the generated type name (default
	// LatchwireClient). See golang.Options.ClientName.
	ClientName string
}

// GenerateOptions selects which language clients Server.Generate produces.
// Leave a field nil to skip that language.
type GenerateOptions struct {
	TypeScript *TypeScriptOptions
	Dart       *DartOptions
	Go         *GoOptions
}

// Generate finalizes the server (if not already finalized) and writes
// generated client code for every requested language. Generation is
// deterministic and atomic per output directory: files are fully rendered
// in memory first, and a directory is only touched if every requested
// language rendered successfully. Latchwire tracks which files it owns in
// each output directory (via a small manifest file) so that a type or
// method removed from the protocol has its stale generated file removed
// too, without ever touching files Latchwire didn't generate.
func (s *Server[S]) Generate(opts GenerateOptions) error {
	if err := s.finalize(); err != nil {
		return err
	}

	s.mu.Lock()
	ir := s.ir
	s.mu.Unlock()

	if opts.TypeScript != nil {
		files, err := typescript.Generate(ir, typescript.Options{ClientName: opts.TypeScript.ClientName})
		if err != nil {
			return fmt.Errorf("latchwire: generate typescript: %w", err)
		}
		if err := writeOwnedFiles(opts.TypeScript.OutputDir, files); err != nil {
			return fmt.Errorf("latchwire: write typescript output: %w", err)
		}
	}

	if opts.Dart != nil {
		files, err := dart.Generate(ir, dart.Options{
			Package:    opts.Dart.Package,
			ClientName: opts.Dart.ClientName,
		})
		if err != nil {
			return fmt.Errorf("latchwire: generate dart: %w", err)
		}
		if err := writeOwnedFiles(opts.Dart.OutputDir, files); err != nil {
			return fmt.Errorf("latchwire: write dart output: %w", err)
		}
		dart.FormatDir(opts.Dart.OutputDir)
	}

	if opts.Go != nil {
		files, err := golang.Generate(ir, golang.Options{
			Package:    opts.Go.Package,
			ClientName: opts.Go.ClientName,
		})
		if err != nil {
			return fmt.Errorf("latchwire: generate go: %w", err)
		}
		if err := writeOwnedFiles(opts.Go.OutputDir, files); err != nil {
			return fmt.Errorf("latchwire: write go output: %w", err)
		}
	}

	return nil
}

// ownershipManifestName is the file Latchwire uses, inside each output
// directory, to remember which files it generated last time — so a
// subsequent generation can remove files for methods/types/events that no
// longer exist without ever deleting a file it didn't create itself.
const ownershipManifestName = ".latchwire-manifest.json"

type ownershipManifest struct {
	Files []string `json:"files"`
}

// writeOwnedFiles writes files (relative path -> contents) into dir,
// creating it if necessary, then removes any file dir's previous
// ownership manifest listed that is not present in files this time.
func writeOwnedFiles(dir string, files map[string][]byte) error {
	if dir == "" {
		return fmt.Errorf("output directory must not be empty")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	var previous ownershipManifest
	if raw, err := os.ReadFile(filepath.Join(dir, ownershipManifestName)); err == nil {
		_ = json.Unmarshal(raw, &previous)
	}

	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		tmp := path + ".tmp"
		if err := os.WriteFile(tmp, files[name], 0o644); err != nil {
			return err
		}
		if err := os.Rename(tmp, path); err != nil {
			return err
		}
	}

	current := map[string]bool{}
	for _, name := range names {
		current[name] = true
	}
	for _, name := range previous.Files {
		if !current[name] {
			_ = os.Remove(filepath.Join(dir, filepath.FromSlash(name)))
		}
	}

	manifest := ownershipManifest{Files: names}
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, ownershipManifestName), raw, 0o644)
}
