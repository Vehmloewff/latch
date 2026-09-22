package main

import (
	"net"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestLanguagesSelectsAllByDefaultAndRequestedSubset(t *testing.T) {
	all := languages(nil)
	for _, language := range []string{"go", "dart", "typescript"} {
		if !all[language] {
			t.Errorf("default languages missing %q: %v", language, all)
		}
	}

	selected := languages([]string{"go", "go", "typescript"})
	if !selected["go"] || !selected["typescript"] || selected["dart"] {
		t.Fatalf("languages subset = %v, want go/typescript only", selected)
	}
}

func TestLanguagesRejectsUnknownLanguage(t *testing.T) {
	defer func() {
		if recovered := recover(); recovered == nil {
			t.Fatal("languages did not panic for unknown language")
		}
	}()
	languages([]string{"ruby"})
}

func TestIsDirDistinguishesDirectoriesFilesAndMissingPaths(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !isDir(dir) {
		t.Fatal("isDir(temp directory) = false, want true")
	}
	if isDir(file) {
		t.Fatal("isDir(file) = true, want false")
	}
	if isDir(filepath.Join(dir, "missing")) {
		t.Fatal("isDir(missing path) = true, want false")
	}
}

func TestDiscoverExamplesFindsExpectedSortedExampleDirectories(t *testing.T) {
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "..", ".."))
	oldWorkingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(repoRoot); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWorkingDirectory) })

	examples := discoverExamples()
	if len(examples) == 0 {
		t.Fatal("discoverExamples returned no examples")
	}
	for i := range examples {
		if !isDir(filepath.Join(examples[i].dir, "cmd", "generate_clients")) || !isDir(filepath.Join(examples[i].dir, "cmd", "server")) {
			t.Fatalf("example %q does not have required command directories", examples[i])
		}
		if i > 0 && examples[i-1].name > examples[i].name {
			t.Fatalf("examples are not sorted: %v", examples)
		}
	}
}

func TestWaitForPortReturnsWhenTCPPortIsAvailable(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	waitForPort(listener.Addr().String())
}
