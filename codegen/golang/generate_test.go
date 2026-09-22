package golang

import (
	"bytes"
	"reflect"
	"testing"
	"time"

	"github.com/vehmloewff/latch/protocol"
	"github.com/vehmloewff/latch/reflectapi"
)

type ConnectParams struct {
	Token string `latch:"1"`
}

type InvoiceStatus string

type Invoice struct {
	ID        string            `latch:"1"`
	Status    InvoiceStatus     `latch:"2" jsonschema_enum:"draft,sent,paid"`
	Amount    int               `latch:"3"`
	Note      *string           `latch:"4,omitempty"`
	Tags      []string          `latch:"5"`
	Metadata  map[string]string `latch:"6"`
	CreatedAt time.Time         `latch:"7"`
}

type GetInvoiceRequest struct {
	ID string `latch:"1"`
}

type GetInvoiceResponse struct {
	Invoice Invoice `latch:"1"`
}

type UserGetRequest struct {
	ID string `latch:"1"`
}

type UserGetResponse struct {
	Name string `latch:"1"`
}

type InvoiceUpdated struct {
	Invoice Invoice `latch:"1"`
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

	return &protocol.Protocol{
		Name:        "billing",
		Version:     "1",
		ConnectType: connectRef,
		Methods: []protocol.Method{
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

	for _, name := range []string{"client.go"} {
		if _, ok := files[name]; !ok {
			t.Fatalf("expected file %q in generated output", name)
		}
	}

	types := string(files["client.go"])
	wantTypeSnippets := []string{
		`type Invoice struct {`,
		`Amount`,
		`int`,
		`Note`,
		`*string`,
		`Tags`,
		`[]string`,
		`Metadata`,
		`map[string]string`,
		`CreatedAt`,
		`time.Time`,
		`"time"`,
		`type InvoiceStatus string`,
		`InvoiceStatusDraft`,
		`= "draft"`,
		`InvoiceStatusSent`,
		`= "sent"`,
		`InvoiceStatusPaid`,
		`= "paid"`,
	}
	for _, want := range wantTypeSnippets {
		if !bytes.Contains([]byte(types), []byte(want)) {
			t.Errorf("types.go missing expected snippet %q; got:\n%s", want, types)
		}
	}

	client := string(files["client.go"])
	wantClientSnippets := []string{
		`type LatchClient struct {`,
		`type ConnectedLatchClient struct {`,
		`func (c *ConnectedLatchClient) Get(ctx context.Context, req GetInvoiceRequest)`,
		`client.Call[GetInvoiceResponse](ctx, c.conn, "billing.invoice.get", req)`,
		`client.Call[UserGetResponse](ctx, c.conn, "user.get", req)`,
		`client.RegisterEvent[InvoiceUpdated](conn)`,
		`func (c *ConnectedLatchClient) Close() error {`,
	}
	for _, want := range wantClientSnippets {
		if !bytes.Contains([]byte(client), []byte(want)) {
			t.Errorf("client.go missing expected snippet %q; got:\n%s", want, client)
		}
	}
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

func TestEventGetterCollisionRejected(t *testing.T) {
	p := buildFixtureProtocol(t)
	p.Events = append(p.Events, protocol.Event{
		Name:        "billingInvoiceUpdated",
		PayloadType: p.Events[0].PayloadType,
	})

	if _, err := Generate(p, Options{}); err == nil {
		t.Fatalf("expected event getter name collision to be rejected")
	}
}
