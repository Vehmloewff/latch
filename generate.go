package latch

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/vehmloewff/latch/codegen/dart"
	"github.com/vehmloewff/latch/codegen/golang"
	"github.com/vehmloewff/latch/codegen/kotlin"
	"github.com/vehmloewff/latch/codegen/swift"
	"github.com/vehmloewff/latch/codegen/typescript"
	"github.com/vehmloewff/latch/protocol"
)

// TypeScriptOptions configures TypeScript client generation.
type TypeScriptOptions struct {
	// OutputDir is the directory the generated TypeScript source file is
	// written to, created if it does not already exist.
	OutputDir string

	// ClientName overrides the generated class name (default
	// LatchClient). See typescript.Options.ClientName.
	ClientName string
}

// DartOptions configures Dart client generation.
type DartOptions struct {
	// OutputDir is the directory the generated Dart source file is written
	// to, created if it does not already exist.
	OutputDir string

	// Package overrides the generated pubspec/barrel-file package name
	// (default latch_client). See dart.Options.Package.
	Package string

	// ClientName overrides the generated class name (default
	// LatchClient). See dart.Options.ClientName.
	ClientName string
}

// GoOptions configures Go client generation.
type GoOptions struct {
	// OutputDir is the directory the generated Go source file is written to,
	// created if it does not already exist. It is typically a subdirectory of
	// the consuming Go module, since generated output is plain source, not a
	// separate module.
	OutputDir string

	// Package overrides the generated package name (default
	// latchclient). See golang.Options.Package.
	Package string

	// ClientName overrides the generated type name (default
	// LatchClient). See golang.Options.ClientName.
	ClientName string
}

// SwiftOptions configures Swift client generation.
type SwiftOptions struct {
	// OutputDir is the directory the generated Swift source file is written to.
	OutputDir string
	// ClientName overrides the generated client class name (default LatchClient).
	ClientName string
}

// KotlinOptions configures Kotlin/JVM client generation.
type KotlinOptions struct {
	// OutputDir is the directory for the standalone LatchClient.kt source file.
	OutputDir string
	// ClientName overrides the generated client class name (default LatchClient).
	ClientName string
}

// GenerateOptions selects which language clients Server.Generate produces.
// Leave a field nil to skip that language.
type GenerateOptions struct {
	TypeScript *TypeScriptOptions
	Dart       *DartOptions
	Go         *GoOptions
	Swift      *SwiftOptions
	Kotlin     *KotlinOptions
}

// Schema finalizes the server (if necessary) and returns the normalized
// protocol IR consumed by client generators. It is an in-process Go value;
// Latch does not serialize it as a manifest or use it for wire validation.
func (s *Server[S]) Schema() (*protocol.Protocol, error) {
	if err := s.finalize(); err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ir, nil
}

// GenerateSchema is an explicit alias for Schema retained for source
// compatibility. It returns the in-process protocol IR.
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
		return fmt.Errorf("latch: generate typescript: %w", err)
	}
	if err := writeGeneratedFiles(opts.OutputDir, files); err != nil {
		return fmt.Errorf("latch: write typescript output: %w", err)
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
		return fmt.Errorf("latch: generate dart: %w", err)
	}
	if err := writeGeneratedFiles(opts.OutputDir, files); err != nil {
		return fmt.Errorf("latch: write dart output: %w", err)
	}

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
		return fmt.Errorf("latch: generate go: %w", err)
	}
	if err := writeGeneratedFiles(opts.OutputDir, files); err != nil {
		return fmt.Errorf("latch: write go output: %w", err)
	}
	return nil
}

// GenerateSwift finalizes the server and writes a standalone Swift client.
func (s *Server[S]) GenerateSwift(opts SwiftOptions) error {
	schema, err := s.Schema()
	if err != nil {
		return err
	}
	files, err := swift.Generate(schema, swift.Options{ClientName: opts.ClientName})
	if err != nil {
		return fmt.Errorf("latch: generate swift: %w", err)
	}
	if err := writeGeneratedFiles(opts.OutputDir, files); err != nil {
		return fmt.Errorf("latch: write swift output: %w", err)
	}
	return nil
}

// GenerateKotlin finalizes the server and writes a standalone Kotlin/JVM client.
func (s *Server[S]) GenerateKotlin(opts KotlinOptions) error {
	schema, err := s.Schema()
	if err != nil {
		return err
	}
	files, err := kotlin.Generate(schema, kotlin.Options{ClientName: opts.ClientName})
	if err != nil {
		return fmt.Errorf("latch: generate kotlin: %w", err)
	}
	if err := writeGeneratedFiles(opts.OutputDir, files); err != nil {
		return fmt.Errorf("latch: write kotlin output: %w", err)
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
	if opts.Swift != nil {
		if err := s.GenerateSwift(*opts.Swift); err != nil {
			return err
		}
	}
	if opts.Kotlin != nil {
		if err := s.GenerateKotlin(*opts.Kotlin); err != nil {
			return err
		}
	}
	return nil
}

// writeGeneratedFiles writes the files returned by a code generator into dir.
// Each generated file is rendered completely in memory before it is moved into
// place, so a failed write never leaves a partial file.
func writeGeneratedFiles(dir string, files map[string][]byte) error {
	if dir == "" {
		return fmt.Errorf("output directory must not be empty")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
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
	return nil
}
