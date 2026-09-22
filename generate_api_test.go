package latch

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vehmloewff/latch/protocol"
)

type generationTestState struct{}

type generationTestEvent struct {
	Kind string `latch:"1"`
}
type generationTestRequest struct {
	Value int `latch:"1"`
}
type generationTestResponse struct {
	Value int `latch:"1"`
}

func newGenerationTestServer(t *testing.T) *Server[generationTestState] {
	t.Helper()
	server := New[generationTestState](Options{ProtocolVersion: "api-v1"})
	server.OnConnect(func(context.Context, Emitter[generationTestEvent], *Conn) (generationTestState, error) {
		return generationTestState{}, nil
	})
	server.Register("math_add", func(_ context.Context, _ generationTestState, req generationTestRequest) (generationTestResponse, error) {
		return generationTestResponse{Value: req.Value + 1}, nil
	})
	return server
}

func TestGenerateSchemaPublicAPI(t *testing.T) {
	server := newGenerationTestServer(t)

	first, err := server.GenerateSchema()
	if err != nil {
		t.Fatalf("GenerateSchema: %v", err)
	}
	second, err := server.Schema()
	if err != nil {
		t.Fatalf("Schema after GenerateSchema: %v", err)
	}
	if first != second {
		t.Fatal("GenerateSchema and Schema returned different protocol pointers")
	}
	if first.Version != "api-v1" {
		t.Fatalf("schema version = %q, want api-v1", first.Version)
	}
	if len(first.Methods) != 1 || first.Methods[0].Name != "math_add" {
		t.Fatalf("schema methods = %#v, want one math_add method", first.Methods)
	}
	if first.EventType.Kind != protocol.KindStruct {
		t.Fatalf("schema event kind = %q, want struct", first.EventType.Kind)
	}
	if len(first.Types) == 0 {
		t.Fatal("schema contains no reachable types")
	}
}

func TestTargetSpecificGenerationWritesExpectedFiles(t *testing.T) {
	tests := []struct {
		name string
		call func(*Server[generationTestState], string) error
		file string
		want []string
	}{
		{
			name: "typescript",
			call: func(s *Server[generationTestState], dir string) error {
				return s.GenerateTypeScript(TypeScriptOptions{OutputDir: dir, ClientName: "WebClient"})
			},
			file: "client.ts",
			want: []string{"export class WebClient {", "mathAdd"},
		},
		{
			name: "dart",
			call: func(s *Server[generationTestState], dir string) error {
				return s.GenerateDart(DartOptions{OutputDir: dir, Package: "api_client", ClientName: "WebClient"})
			},
			file: "lib/client.dart",
			want: []string{"class WebClient {", "api-v1"},
		},
		{
			name: "go",
			call: func(s *Server[generationTestState], dir string) error {
				return s.GenerateGo(GoOptions{OutputDir: dir, Package: "apiclient", ClientName: "WebClient"})
			},
			file: "client.go",
			want: []string{"package apiclient", "type WebClient struct", "math_add"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "generated")
			if err := tt.call(newGenerationTestServer(t), dir); err != nil {
				t.Fatalf("generate: %v", err)
			}
			path := filepath.Join(dir, filepath.FromSlash(tt.file))
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("ReadFile(%q): %v", path, err)
			}
			for _, want := range tt.want {
				if !strings.Contains(string(data), want) {
					t.Errorf("%s missing %q", tt.file, want)
				}
			}
		})
	}
}

func TestGenerateWritesAllRequestedTargetsAndSkipsNilTargets(t *testing.T) {
	root := t.TempDir()
	tsDir := filepath.Join(root, "typescript")
	dartDir := filepath.Join(root, "dart")
	goDir := filepath.Join(root, "go")

	server := newGenerationTestServer(t)
	if err := server.Generate(GenerateOptions{
		TypeScript: &TypeScriptOptions{OutputDir: tsDir},
		Dart:       &DartOptions{OutputDir: dartDir},
		Go:         &GoOptions{OutputDir: goDir},
	}); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	for _, path := range []string{
		filepath.Join(tsDir, "client.ts"),
		filepath.Join(dartDir, "lib", "client.dart"),
		filepath.Join(goDir, "client.go"),
	} {
		if info, err := os.Stat(path); err != nil || info.IsDir() {
			t.Fatalf("generated output %q is not a file: %v", path, err)
		}
	}

	if err := newGenerationTestServer(t).Generate(GenerateOptions{}); err != nil {
		t.Fatalf("Generate with no targets: %v", err)
	}
}

func TestGenerationAPIReportsSchemaAndOutputErrors(t *testing.T) {
	t.Run("missing event type", func(t *testing.T) {
		server := New[generationTestState](Options{})
		if _, err := server.GenerateSchema(); err == nil || !strings.Contains(err.Error(), "event type") {
			t.Fatalf("GenerateSchema error = %v, want event type error", err)
		}
		for _, call := range []struct {
			name string
			fn   func() error
		}{
			{"typescript", func() error { return server.GenerateTypeScript(TypeScriptOptions{OutputDir: t.TempDir()}) }},
			{"dart", func() error { return server.GenerateDart(DartOptions{OutputDir: t.TempDir()}) }},
			{"go", func() error { return server.GenerateGo(GoOptions{OutputDir: t.TempDir()}) }},
			{"all", func() error { return server.Generate(GenerateOptions{Go: &GoOptions{OutputDir: t.TempDir()}}) }},
		} {
			t.Run(call.name, func(t *testing.T) {
				if err := call.fn(); err == nil || !strings.Contains(err.Error(), "event type") {
					t.Fatalf("error = %v, want event type error", err)
				}
			})
		}
	})

	for _, tt := range []struct {
		name string
		call func(*Server[generationTestState], string) error
		want string
	}{
		{"typescript output", func(s *Server[generationTestState], dir string) error {
			return s.GenerateTypeScript(TypeScriptOptions{OutputDir: dir})
		}, "write typescript output"},
		{"dart output", func(s *Server[generationTestState], dir string) error {
			return s.GenerateDart(DartOptions{OutputDir: dir})
		}, "write dart output"},
		{"go output", func(s *Server[generationTestState], dir string) error {
			return s.GenerateGo(GoOptions{OutputDir: dir})
		}, "write go output"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			badOutput := filepath.Join(t.TempDir(), "not-a-directory")
			if err := os.WriteFile(badOutput, []byte("occupied"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := tt.call(newGenerationTestServer(t), badOutput); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestWriteGeneratedFilesCreatesNestedFilesAndRejectsInvalidOutput(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "out")
	files := map[string][]byte{
		"client.go":     []byte("package generated\n"),
		"lib/src/model": []byte("model\n"),
	}
	if err := writeGeneratedFiles(dir, files); err != nil {
		t.Fatalf("writeGeneratedFiles: %v", err)
	}
	for name, want := range files {
		got, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
		if err != nil {
			t.Fatalf("ReadFile(%q): %v", name, err)
		}
		if string(got) != string(want) {
			t.Errorf("%q = %q, want %q", name, got, want)
		}
	}

	if err := writeGeneratedFiles("", files); err == nil || !strings.Contains(err.Error(), "output directory") {
		t.Fatalf("empty directory error = %v, want output directory error", err)
	}
	occupied := filepath.Join(t.TempDir(), "occupied")
	if err := os.WriteFile(occupied, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeGeneratedFiles(occupied, files); err == nil {
		t.Fatal("writeGeneratedFiles succeeded with a file as output directory")
	}
}
