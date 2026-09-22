package dart

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFormatDirIsNoOpWhenDartIsUnavailable(t *testing.T) {
	bin := t.TempDir()
	t.Setenv("PATH", bin)

	FormatDir(t.TempDir())
}

func TestFormatDirRunsDartFormatInPackageDirectory(t *testing.T) {
	bin := t.TempDir()
	packageDir := t.TempDir()
	if err := os.Mkdir(filepath.Join(packageDir, "lib"), 0o755); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(t.TempDir(), "format.log")

	dartPath := filepath.Join(bin, "dart")
	script := "#!/bin/sh\nprintf '%s|%s|%s\\n' \"$PWD\" \"$1\" \"$2\" > \"" + logPath + "\"\n"
	if err := os.WriteFile(dartPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)

	FormatDir(packageDir)

	contents, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("fake dart was not invoked: %v", err)
	}
	want := packageDir + "|format|lib\n"
	if string(contents) != want {
		t.Fatalf("dart invocation = %q, want %q", contents, want)
	}

	// Formatting is intentionally best-effort: a failing formatter must not
	// make generation fail.
	if err := os.WriteFile(dartPath, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	FormatDir(packageDir)
}
