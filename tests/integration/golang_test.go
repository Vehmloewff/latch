package integration

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vehmloewff/latch"
	"github.com/vehmloewff/latch/client"
	"github.com/vehmloewff/latch/examples/basic/api"
	basicclient "github.com/vehmloewff/latch/examples/basic/generated/golang"
)

// TestGeneratedGoClientAgainstLiveServer regenerates the "basic" example's
// Go client from the current generator, then drives it — using ordinary Go
// code, no reflection or codegen at the call site — against a live
// instance of the same protocol served over a real WebSocket.
func TestGeneratedGoClientAgainstLiveServer(t *testing.T) {
	root := repoRoot(t)
	outDir := root + "/examples/basic/generated/golang"

	if err := api.Build().Generate(latch.GenerateOptions{
		Go: &latch.GoOptions{OutputDir: outDir, Package: "basicclient"},
	}); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	srv := httptest.NewServer(api.Build())
	defer srv.Close()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	c := basicclient.New(wsURL)
	conn, err := c.Connect(ctx)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer conn.Close()

	for i := 0; i < 2; i++ {
		select {
		case event := <-conn.Events():
			switch event.Kind {
			case "message":
				if event.Message == nil || event.Message.Room != "lobby" || event.Message.Text != "welcome" {
					t.Fatalf("unexpected welcome event: %+v", event)
				}
			case "presence":
				if event.Presence == nil || event.Presence.UserID != "self" || !event.Presence.Online {
					t.Fatalf("unexpected presence event: %+v", event)
				}
			default:
				t.Fatalf("unexpected event variant: %+v", event)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("timed out waiting for event")
		}
	}

	result, err := conn.RoomSubscribe(ctx, basicclient.SubscribeRequest{Room: "general"})
	if err != nil {
		t.Fatalf("Room.Subscribe: %v", err)
	}
	if !result.OK {
		t.Fatalf("expected OK=true, got %+v", result)
	}

	listed, err := conn.RoomList(ctx, basicclient.ListRoomsRequest{})
	if err != nil {
		t.Fatalf("Room.List: %v", err)
	}
	if len(listed.Rooms) != 3 {
		t.Fatalf("expected 3 rooms, got %+v", listed)
	}

	// Nested types + optional/nullable fields.
	profile, err := conn.ProfileGet(ctx, basicclient.ProfileGetRequest{UserID: "alice"})
	if err != nil {
		t.Fatalf("Profile.Get: %v", err)
	}
	if profile.Profile.Name != "User alice" || profile.Profile.Address.City != "Springfield" {
		t.Fatalf("unexpected profile: %+v", profile)
	}
	if profile.Profile.Nickname == nil || profile.Profile.Address.Zip == nil {
		t.Fatalf("expected nickname and zip to be present: %+v", profile)
	}

	noZipProfile, err := conn.ProfileGet(ctx, basicclient.ProfileGetRequest{UserID: "no-zip"})
	if err != nil {
		t.Fatalf("Profile.Get: %v", err)
	}
	if noZipProfile.Profile.Address.Zip != nil {
		t.Fatalf("expected zip to be nil (Go's own omitempty pointer semantics), got %+v", noZipProfile)
	}

	// Application error.
	_, err = conn.ProfileGet(ctx, basicclient.ProfileGetRequest{UserID: "missing"})
	if err == nil {
		t.Fatalf("expected profile.get(userId=missing) to fail")
	}
	var appErr *client.Error
	if !errors.As(err, &appErr) || appErr.Code != "not_found" || appErr.Message != "user not found" {
		t.Fatalf("expected not_found application error, got %v", err)
	}

	// Concurrent calls: fire many requests at once and verify every
	// response matches its own request despite handlers executing
	// concurrently.
	const n = 10
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			userID := fmt.Sprintf("user-%d", i)
			p, err := conn.ProfileGet(ctx, basicclient.ProfileGetRequest{UserID: userID})
			if err != nil {
				errs <- err
				return
			}
			if p.Profile.Name != "User "+userID {
				errs <- fmt.Errorf("concurrent call %d returned wrong profile: %+v", i, p)
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent call failed: %v", err)
		}
	}

}
