// Package integration holds cross-process integration tests that exercise
// a generated client against a live Latchwire Go server, per spec section
// 49 ("Cross-language integration suite"). These tests shell out to real
// toolchains (node, tsc) and are skipped, with a clear reason, when those
// toolchains aren't available in the environment rather than failing the
// whole suite.
package integration

import (
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/vehmloewff/latch"
	"github.com/vehmloewff/latch/examples/basic/api"
)

// repoRoot locates the module root from this test file's own path, so the
// test works regardless of the directory `go test` is invoked from.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not determine test file path")
	}
	// this file lives at <root>/tests/integration/typescript_test.go
	return filepath.Join(filepath.Dir(file), "..", "..")
}

func requireTool(t *testing.T, name string) string {
	t.Helper()
	path, err := exec.LookPath(name)
	if err != nil {
		t.Skipf("skipping: %q not found on PATH", name)
	}
	return path
}

// TestGeneratedTypeScriptClientAgainstLiveServer regenerates the "basic"
// example's TypeScript client from the current generator, compiles it with
// tsc, and drives it against a live instance of the same protocol served
// over a real WebSocket — the strongest available check that generated
// output and the runtime it targets actually agree with each other.
func TestGeneratedTypeScriptClientAgainstLiveServer(t *testing.T) {
	node := requireTool(t, "node")
	root := repoRoot(t)
	tsDir := filepath.Join(root, "examples", "basic", "generated", "typescript")

	tsc := filepath.Join(tsDir, "node_modules", ".bin", "tsc")
	if _, err := os.Stat(tsc); err != nil {
		t.Skipf("skipping: %s not found; run `npm install` in %s first", tsc, tsDir)
	}

	// Regenerate from the current generator so this test catches
	// regressions in codegen itself, not just in previously-committed
	// output.
	if err := api.Build().Generate(latchwire.GenerateOptions{
		TypeScript: &latchwire.TypeScriptOptions{OutputDir: tsDir},
	}); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	runIn := func(dir, name string, args ...string) {
		t.Helper()
		cmd := exec.Command(name, args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s %s failed: %v\n%s", name, strings.Join(args, " "), err, out)
		}
	}

	// Strict-mode type check, including the compile-failure fixtures
	// (fixtures.badusage.ts) and the golden-path usage example.
	runIn(tsDir, tsc)

	// Compile to plain JS for execution against the live server.
	runIn(tsDir, tsc, "-p", "tsconfig.build.json")

	srv := httptest.NewServer(api.Build())
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"

	cmd := exec.Command(node, "integration-test.cjs")
	cmd.Dir = tsDir
	cmd.Env = append(os.Environ(), "LATCHWIRE_WS_URL="+wsURL)
	out, err := cmd.CombinedOutput()
	t.Logf("node output:\n%s", out)
	if err != nil {
		t.Fatalf("generated TypeScript client integration test failed: %v", err)
	}
	if !strings.Contains(string(out), "OK: TypeScript generated client integration test passed") {
		t.Fatalf("expected success marker in node output, got:\n%s", out)
	}
}
