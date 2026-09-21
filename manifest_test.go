package latchwire_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/vehmloewff/latch"
	"github.com/vehmloewff/report"
)

func buildManifestFixtureServer(t *testing.T) *latchwire.Server[ConnectParams] {
	t.Helper()
	srv := latchwire.New[ConnectParams](latchwire.Options{ProtocolName: "demo", ProtocolVersion: "1"})

	tick := latchwire.Event[Tick]("tick")
	if err := srv.RegisterEvent(tick); err != nil {
		t.Fatalf("RegisterEvent: %v", err)
	}
	err := srv.Register("math.add", func(ctx context.Context, conn *latchwire.Conn[ConnectParams], req AddRequest) (AddResponse, report.Err) {
		return AddResponse{Result: req.A + req.B}, nil
	})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	return srv
}

// TestManifestIsDeterministic verifies spec section 32: the same
// registrations always produce byte-identical manifest JSON, both across
// repeated calls on one server and across two independently built servers.
func TestManifestIsDeterministic(t *testing.T) {
	srv1 := buildManifestFixtureServer(t)
	srv2 := buildManifestFixtureServer(t)

	var buf1, buf2, buf1Again bytes.Buffer
	if err := srv1.WriteManifest(&buf1); err != nil {
		t.Fatalf("WriteManifest 1: %v", err)
	}
	if err := srv1.WriteManifest(&buf1Again); err != nil {
		t.Fatalf("WriteManifest 1 (again): %v", err)
	}
	if err := srv2.WriteManifest(&buf2); err != nil {
		t.Fatalf("WriteManifest 2: %v", err)
	}

	if buf1.String() != buf1Again.String() {
		t.Fatalf("repeated WriteManifest on the same server produced different output")
	}
	if buf1.String() != buf2.String() {
		t.Fatalf("two independently built, identically registered servers produced different manifests")
	}
}
