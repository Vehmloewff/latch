package latch_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/vehmloewff/latch"
)

type manifestState struct{}
type manifestRequest struct {
	Value string `json:"value"`
}
type manifestResponse struct {
	Value string `json:"value"`
}
type manifestEvent struct {
	Kind string `json:"kind"`
}

func buildManifestFixtureServer(t *testing.T) *latch.Server[manifestState] {
	t.Helper()
	server := latch.New[manifestState](latch.Options{ProtocolVersion: "1"})
	server.OnConnect(func(context.Context, latch.Emitter[manifestEvent], *latch.Conn) (manifestState, error) {
		return manifestState{}, nil
	})
	server.Register("echo_value", func(context.Context, manifestState, manifestRequest) (manifestResponse, error) {
		return manifestResponse{}, nil
	})
	return server
}

func TestManifestIsDeterministic(t *testing.T) {
	server1 := buildManifestFixtureServer(t)
	server2 := buildManifestFixtureServer(t)

	var first, second, repeat bytes.Buffer
	for name, server := range map[string]*latch.Server[manifestState]{"first": server1, "second": server2} {
		var target *bytes.Buffer
		if name == "first" {
			target = &first
		} else {
			target = &second
		}
		if err := server.WriteManifest(target); err != nil {
			t.Fatalf("WriteManifest %s: %v", name, err)
		}
	}
	if err := server1.WriteManifest(&repeat); err != nil {
		t.Fatalf("WriteManifest repeat: %v", err)
	}
	if first.String() != repeat.String() || first.String() != second.String() {
		t.Fatal("identical servers produced different manifests")
	}
}
