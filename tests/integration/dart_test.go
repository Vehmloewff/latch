package integration

import (
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vehmloewff/latch"
	"github.com/vehmloewff/latch/examples/basic/api"
)

// TestGeneratedDartClientAgainstLiveServer regenerates the "basic"
// example's Dart client from the current generator, resolves its pub
// dependencies, and drives it against a live instance of the same protocol
// served over a real WebSocket.
func TestGeneratedDartClientAgainstLiveServer(t *testing.T) {
	dartBin := requireTool(t, "dart")
	root := repoRoot(t)
	dartDir := filepath.Join(root, "examples", "basic", "generated", "dart")

	if _, err := os.Stat(filepath.Join(dartDir, ".dart_tool")); err != nil {
		t.Skipf("skipping: %s/.dart_tool not found; run `dart pub get` in %s first", dartDir, dartDir)
	}

	if err := api.Build().Generate(latchwire.GenerateOptions{
		Dart: &latchwire.DartOptions{OutputDir: dartDir, Package: "basic_client"},
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

	// Analyze only the generated library and the hand-written integration
	// test — not test/type_safety_fixture.dart, which is intentionally
	// invalid and checked separately below.
	runIn(dartDir, dartBin, "analyze", "lib", "test/integration_test.dart")

	// Compile-time type-safety fixture (spec section 48): every one of
	// these usages must fail analysis. Assert the exact analyzer error
	// codes rather than just "analyze exits non-zero", so a generated API
	// that silently degraded to `dynamic` (which would make some of these
	// lines stop erroring) is still caught even if others still fail.
	fixtureCmd := exec.Command(dartBin, "analyze", "test/type_safety_fixture.dart")
	fixtureCmd.Dir = dartDir
	fixtureOut, fixtureErr := fixtureCmd.CombinedOutput()
	if fixtureErr == nil {
		t.Fatalf("expected test/type_safety_fixture.dart to fail analysis, but it passed:\n%s", fixtureOut)
	}
	for _, wantCode := range []string{
		"argument_type_not_assignable",
		"undefined_getter",
		"missing_required_argument",
	} {
		if !strings.Contains(string(fixtureOut), wantCode) {
			t.Errorf("expected analyzer error code %q in fixture output, got:\n%s", wantCode, fixtureOut)
		}
	}

	srv := httptest.NewServer(api.Build())
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"

	cmd := exec.Command(dartBin, "test")
	cmd.Dir = dartDir
	cmd.Env = append(os.Environ(), "LATCHWIRE_WS_URL="+wsURL)
	out, err := cmd.CombinedOutput()
	t.Logf("dart test output:\n%s", out)
	if err != nil {
		t.Fatalf("generated Dart client integration test failed: %v", err)
	}
	if !strings.Contains(string(out), "All tests passed!") {
		t.Fatalf("expected success marker in dart test output, got:\n%s", out)
	}
}
