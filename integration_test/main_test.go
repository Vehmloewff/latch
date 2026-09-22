package main

import (
	"net"
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

func TestWaitForPortReturnsWhenTCPPortIsAvailable(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	waitForPort(listener.Addr().String())
}
