package dart

import "os/exec"

// FormatDir runs `dart format lib` inside dir (a generated Dart package
// root containing pubspec.yaml), so the formatter picks up the package's
// SDK constraint and applies the same style a consumer running `dart
// format` themselves would see — running it stdin-to-stdout without that
// package context can pick a different formatting style. It only formats
// "lib", the directory Latchwire actually generates into, so a hand-written
// file elsewhere in the package (e.g. under test/) is never touched. If the
// dart binary isn't on PATH, or formatting fails for any reason, this is a
// no-op: generated output is always valid Dart either way, and formatting
// is cosmetic.
func FormatDir(dir string) {
	path, err := exec.LookPath("dart")
	if err != nil {
		return
	}
	cmd := exec.Command(path, "format", "lib")
	cmd.Dir = dir
	_ = cmd.Run()
}
