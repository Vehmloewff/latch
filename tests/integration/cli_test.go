package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/vehmloewff/latchwire/examples/basic/api"
)

// TestCLIGenerateFromManifest verifies the secondary, manifest-consuming
// generation path (spec section 20): export a manifest via
// Server.WriteManifest, then run `latchwire generate` as a separate
// process against only that file — no Go reflection, no access to the
// server's source — and confirm it produces a working Go client.
func TestCLIGenerateFromManifest(t *testing.T) {
	root := repoRoot(t)

	// Created inside the module tree (not t.TempDir(), which lives under
	// the OS temp dir) so the generated Go package resolves as part of
	// this module and `go build` can find its dependencies without a
	// separate go.mod.
	tmp, err := os.MkdirTemp(root, ".cli-test-tmp-")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(tmp) })

	manifestPath := filepath.Join(tmp, "manifest.json")
	f, err := os.Create(manifestPath)
	if err != nil {
		t.Fatalf("create manifest file: %v", err)
	}
	if err := api.Build().WriteManifest(f); err != nil {
		f.Close()
		t.Fatalf("WriteManifest: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close manifest file: %v", err)
	}

	goOut := filepath.Join(tmp, "go")
	cmd := exec.Command(
		"go", "run", "./cmd/latchwire", "generate",
		"--manifest", manifestPath,
		"--go", goOut,
		"--go-package", "cligenclient",
	)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("latchwire generate: %v\n%s", err, out)
	}

	for _, name := range []string{"types.go", "client.go"} {
		if _, err := os.Stat(filepath.Join(goOut, name)); err != nil {
			t.Fatalf("expected %s to be generated: %v", name, err)
		}
	}

	// Confirm the generated package actually compiles as part of this
	// module (it imports github.com/vehmloewff/latchwire/client, already a
	// dependency here). The generated dir has no go.mod of its own, so
	// build it via its path relative to the module root.
	relOut, err := filepath.Rel(root, goOut)
	if err != nil {
		t.Fatalf("rel: %v", err)
	}
	buildCmd := exec.Command("go", "build", "./"+filepath.ToSlash(relOut)+"/...")
	buildCmd.Dir = root
	out, err = buildCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go build generated CLI output: %v\n%s", err, out)
	}
}
