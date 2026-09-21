package latchwire

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/vehmloewff/latch/codegen/dart"
	"github.com/vehmloewff/latch/codegen/golang"
	"github.com/vehmloewff/latch/codegen/typescript"
	"github.com/vehmloewff/latch/protocol"
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

// Schema finalizes the server (if necessary) and returns the normalized
// protocol schema used by runtime validation and client generators. The
// returned schema is immutable from the server's point of view and should be
// treated as read-only by callers.
func (s *Server[S]) Schema() (*protocol.Protocol, error) {
	if err := s.finalize(); err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ir, nil
}

// GenerateSchema is an explicit alias for Schema. It is useful when the
// schema is being obtained as an input to custom tooling or a custom client
// generator.
func (s *Server[S]) GenerateSchema() (*protocol.Protocol, error) {
	return s.Schema()
}

// GenerateTypeScript finalizes the server and writes a TypeScript client.
// The server's generated schema is passed directly to the TypeScript
// codegen package.
func (s *Server[S]) GenerateTypeScript(opts TypeScriptOptions) error {
	schema, err := s.Schema()
	if err != nil {
		return err
	}
	files, err := typescript.Generate(schema, typescript.Options{ClientName: opts.ClientName})
	if err != nil {
		return fmt.Errorf("latchwire: generate typescript: %w", err)
	}
	if err := writeOwnedFiles(opts.OutputDir, files); err != nil {
		return fmt.Errorf("latchwire: write typescript output: %w", err)
	}
	return nil
}

// GenerateDart finalizes the server and writes a Dart client package. The
// server's generated schema is passed directly to the Dart codegen package.
func (s *Server[S]) GenerateDart(opts DartOptions) error {
	schema, err := s.Schema()
	if err != nil {
		return err
	}
	files, err := dart.Generate(schema, dart.Options{
		Package:    opts.Package,
		ClientName: opts.ClientName,
	})
	if err != nil {
		return fmt.Errorf("latchwire: generate dart: %w", err)
	}
	if err := writeOwnedFiles(opts.OutputDir, files); err != nil {
		return fmt.Errorf("latchwire: write dart output: %w", err)
	}
	dart.FormatDir(opts.OutputDir)
	return nil
}

// GenerateGo finalizes the server and writes a Go client. The server's
// generated schema is passed directly to the Go codegen package.
func (s *Server[S]) GenerateGo(opts GoOptions) error {
	schema, err := s.Schema()
	if err != nil {
		return err
	}
	files, err := golang.Generate(schema, golang.Options{
		Package:    opts.Package,
		ClientName: opts.ClientName,
	})
	if err != nil {
		return fmt.Errorf("latchwire: generate go: %w", err)
	}
	if err := writeOwnedFiles(opts.OutputDir, files); err != nil {
		return fmt.Errorf("latchwire: write go output: %w", err)
	}
	return nil
}

// Generate finalizes the server (if not already finalized) and writes
// generated client code for every requested language. It is retained as a
// convenience wrapper; callers that want one target can use
// GenerateTypeScript, GenerateDart, or GenerateGo directly.
func (s *Server[S]) Generate(opts GenerateOptions) error {
	if opts.TypeScript != nil {
		if err := s.GenerateTypeScript(*opts.TypeScript); err != nil {
			return err
		}
	}
	if opts.Dart != nil {
		if err := s.GenerateDart(*opts.Dart); err != nil {
			return err
		}
	}
	if opts.Go != nil {
		if err := s.GenerateGo(*opts.Go); err != nil {
			return err
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
