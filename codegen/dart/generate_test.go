package dart

import (
	"bytes"
	"reflect"
	"testing"

	"github.com/vehmloewff/latch/protocol"
	"github.com/vehmloewff/latch/reflectapi"
)

type ConnectParams struct {
	Token string `json:"token"`
}

type InvoiceStatus string

type Invoice struct {
	ID       string            `json:"id"`
	Status   InvoiceStatus     `json:"status" jsonschema_enum:"draft,sent,paid"`
	Amount   int               `json:"amount"`
	Note     *string           `json:"note,omitempty"`
	Tags     []string          `json:"tags"`
	Metadata map[string]string `json:"metadata"`
}

type GetInvoiceRequest struct {
	ID string `json:"id"`
}

type GetInvoiceResponse struct {
	Invoice Invoice `json:"invoice"`
}

type UserGetRequest struct {
	ID string `json:"id"`
}

// EmptyRequest has no fields — Dart cannot use its usual "({...})"
// named-parameter constructor syntax for this case (an empty "{}"
// named-parameter group is itself a syntax error).
type EmptyRequest struct{}

type UserGetResponse struct {
	Name string `json:"name"`
}

type InvoiceUpdated struct {
	Invoice Invoice `json:"invoice"`
}

func buildFixtureProtocol(t *testing.T) *protocol.Protocol {
	t.Helper()
	r := reflectapi.NewRegistry()

	connectRef, err := r.Resolve(reflect.TypeOf(ConnectParams{}))
	if err != nil {
		t.Fatalf("resolve connect: %v", err)
	}
	reqRef, err := r.Resolve(reflect.TypeOf(GetInvoiceRequest{}))
	if err != nil {
		t.Fatalf("resolve request: %v", err)
	}
	respRef, err := r.Resolve(reflect.TypeOf(GetInvoiceResponse{}))
	if err != nil {
		t.Fatalf("resolve response: %v", err)
	}
	userReqRef, err := r.Resolve(reflect.TypeOf(UserGetRequest{}))
	if err != nil {
		t.Fatalf("resolve user request: %v", err)
	}
	userRespRef, err := r.Resolve(reflect.TypeOf(UserGetResponse{}))
	if err != nil {
		t.Fatalf("resolve user response: %v", err)
	}
	evtRef, err := r.Resolve(reflect.TypeOf(InvoiceUpdated{}))
	if err != nil {
		t.Fatalf("resolve event: %v", err)
	}
	emptyRef, err := r.Resolve(reflect.TypeOf(EmptyRequest{}))
	if err != nil {
		t.Fatalf("resolve empty request: %v", err)
	}

	return &protocol.Protocol{
		Name:        "billing",
		Version:     "1",
		ConnectType: connectRef,
		Methods: []protocol.Method{
			{Name: "ping", RequestType: emptyRef, ResponseType: emptyRef},
			{Name: "billing.invoice.get", RequestType: reqRef, ResponseType: respRef},
			{Name: "user.get", RequestType: userReqRef, ResponseType: userRespRef},
		},
		Events: []protocol.Event{
			{Name: "billing.invoice.updated", PayloadType: evtRef},
		},
		Types: r.Types(),
	}
}

func TestGenerateProducesExpectedShapes(t *testing.T) {
	p := buildFixtureProtocol(t)

	files, err := Generate(p, Options{})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	for _, name := range []string{"lib/client.dart"} {
		if _, ok := files[name]; !ok {
			t.Fatalf("expected file %q in generated output; got %v", name, keysOf(files))
		}
	}

	models := string(files["lib/client.dart"])
	wantModelSnippets := []string{
		`class Invoice {`,
		`final int amount;`,
		`final String? note;`,
		`final List<String> tags;`,
		`final Map<String, String> metadata;`,
		`enum InvoiceStatus {`,
		`draft("draft")`,
		`sent("sent")`,
		`paid("paid")`,
		`class EmptyRequest {`,
		`EmptyRequest();`,
		`return EmptyRequest();`,
	}
	for _, want := range wantModelSnippets {
		if !bytes.Contains([]byte(models), []byte(want)) {
			t.Errorf("models.dart missing expected snippet %q; got:\n%s", want, models)
		}
	}

	client := string(files["lib/client.dart"])
	wantClientSnippets := []string{
		`class LatchClient {`,
		`class ConnectedLatchClient extends BaseConnection {`,
		`Future<GetInvoiceResponse> billingInvoiceGet`,
		`Future<UserGetResponse> userGet`,
		`"billing.invoice.get"`,
		`"user.get"`,
	}
	for _, want := range wantClientSnippets {
		if !bytes.Contains([]byte(client), []byte(want)) {
			t.Errorf("client.dart missing expected snippet %q; got:\n%s", want, client)
		}
	}
}

func keysOf(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestGenerateIsDeterministic(t *testing.T) {
	p1 := buildFixtureProtocol(t)
	p2 := buildFixtureProtocol(t)

	files1, err := Generate(p1, Options{})
	if err != nil {
		t.Fatalf("Generate 1: %v", err)
	}
	files2, err := Generate(p2, Options{})
	if err != nil {
		t.Fatalf("Generate 2: %v", err)
	}

	if len(files1) != len(files2) {
		t.Fatalf("file count mismatch: %d vs %d", len(files1), len(files2))
	}
	for name, content1 := range files1 {
		content2, ok := files2[name]
		if !ok {
			t.Fatalf("file %q missing from second generation", name)
		}
		if !bytes.Equal(content1, content2) {
			t.Fatalf("file %q differs between two generations of the same protocol", name)
		}
	}
}

func TestEventPropertyCollisionRejected(t *testing.T) {
	p := buildFixtureProtocol(t)
	p.Events = append(p.Events, protocol.Event{
		Name:        "billingInvoiceUpdated",
		PayloadType: p.Events[0].PayloadType,
	})

	if _, err := Generate(p, Options{}); err == nil {
		t.Fatalf("expected event property name collision to be rejected")
	}
}
