package kotlin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/vehmloewff/latch/wire"
)

func TestGeneratedKotlinRealSocketLifecycle(t *testing.T) {
	kotlinc, err := exec.LookPath("kotlinc")
	if err != nil {
		t.Skip("kotlinc unavailable")
	}
	java, err := exec.LookPath("java")
	if err != nil {
		t.Skip("Java unavailable")
	}
	if _, err = exec.Command(java, "-version").CombinedOutput(); err != nil {
		t.Skip("Java runtime unavailable")
	}
	event, err := wire.Encode(struct {
		Text     string            `latch:"1"`
		State    string            `latch:"2"`
		Nullable *string           `latch:"3"`
		Blob     []byte            `latch:"5"`
		History  []string          `latch:"6"`
		Tags     map[string]string `latch:"7"`
	}{Text: "early", State: "open", Blob: []byte{}, History: []string{}, Tags: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("version") != "v1" || r.URL.Query().Get("foo") != "bar" {
			t.Errorf("version query not preserved/replaced: %s", r.URL.RawQuery)
			http.Error(w, "bad version", 400)
			return
		}
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("accept: %v", err)
			return
		}
		defer ws.Close(websocket.StatusNormalClosure, "done")
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		raw, err := (wire.Envelope{Type: wire.FrameEvent, Payload: event}).MarshalBinary()
		if err != nil {
			t.Errorf("event: %v", err)
			return
		}
		if err = ws.Write(ctx, websocket.MessageBinary, raw); err != nil {
			t.Errorf("write event: %v", err)
			return
		}
		for i := 0; i < 3; i++ {
			typ, bytes, err := ws.Read(ctx)
			if err != nil {
				t.Errorf("read request %d: %v", i, err)
				return
			}
			if typ != websocket.MessageBinary {
				t.Errorf("request not binary")
				return
			}
			var req wire.Envelope
			if err = req.UnmarshalBinary(bytes); err != nil {
				t.Errorf("decode request: %v", err)
				return
			}
			if req.Type != wire.FrameRequest || req.Method != "chat_send_message" {
				t.Errorf("request: %+v", req)
				return
			}
			if i == 2 {
				return
			} // close with one request outstanding
			resp := wire.Envelope{Type: wire.FrameResponse, ID: req.ID, Payload: req.Payload}
			if i == 1 {
				resp = wire.Envelope{Type: wire.FrameError, ID: req.ID, ErrorCode: "denied", Error: "not allowed"}
			}
			raw, err = resp.MarshalBinary()
			if err != nil {
				t.Errorf("marshal response: %v", err)
				return
			}
			if err = ws.Write(ctx, websocket.MessageBinary, raw); err != nil {
				t.Errorf("write response: %v", err)
				return
			}
		}
	})
	server := httptest.NewServer(handler)
	defer server.Close()
	dir := t.TempDir()
	gen := filepath.Join(dir, "LatchClient.kt")
	testFile := filepath.Join(dir, "SocketTest.kt")
	if err = os.WriteFile(gen, []byte(source(t, fixture(), Options{})), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(testFile, []byte(socketHarness), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	jar := filepath.Join(dir, "test.jar")
	if out, err := exec.CommandContext(ctx, kotlinc, gen, testFile, "-include-runtime", "-d", jar).CombinedOutput(); err != nil {
		t.Fatalf("compile Kotlin: %v\n%s", err, out)
	}
	url := "ws" + server.URL[len("http"):] + "?foo=bar&version=old"
	if out, err := exec.CommandContext(ctx, java, "-jar", jar, url).CombinedOutput(); err != nil {
		t.Fatalf("Kotlin socket test: %v\n%s", err, out)
	}
}

const socketHarness = `import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import java.util.concurrent.ExecutionException

fun main(args: Array<String>) {
  val arrived = CountDownLatch(1)
  val states = java.util.concurrent.CopyOnWriteArrayList<ConnectionState>()
  val client = LatchClient(args.single(), { e -> check(e.text == "early"); arrived.countDown() }, { states.add(it) }).connect().get(8, TimeUnit.SECONDS)
  check(states.toList() == listOf(ConnectionState.CONNECTING, ConnectionState.CONNECTED))
  check(arrived.await(4, TimeUnit.SECONDS))
  val packet = Packet("hello", State.Open, null, blob = byteArrayOf(1, 2), history = emptyList(), tags = emptyMap())
  val response = client.chatSendMessage(packet).get(5, TimeUnit.SECONDS)
  check(response.text == "hello" && response.blob.contentEquals(byteArrayOf(1, 2)))
  try { client.chatSendMessage(packet).get(5, TimeUnit.SECONDS); error("RPC error succeeded") }
  catch (e: ExecutionException) { check((e.cause as LatchError).code == "denied") }
  try { client.chatSendMessage(packet).get(5, TimeUnit.SECONDS); error("pending RPC survived server close") }
  catch (_: ExecutionException) { }
  client.close()
  check(states.toList() == listOf(ConnectionState.CONNECTING, ConnectionState.CONNECTED, ConnectionState.OFFLINE))
  try { client.chatSendMessage(packet).get(5, TimeUnit.SECONDS); error("closed connection allowed RPC") }
  catch (_: ExecutionException) { }
  val failedStates = mutableListOf<ConnectionState>()
  try { LatchClient("not a websocket URL", { _ -> }, { failedStates.add(it) }).connect().get(5, TimeUnit.SECONDS); error("invalid URL connected") }
  catch (_: ExecutionException) { }
  check(failedStates == listOf(ConnectionState.CONNECTING, ConnectionState.OFFLINE))
  println("Kotlin real WebSocket lifecycle passed")
}
`
