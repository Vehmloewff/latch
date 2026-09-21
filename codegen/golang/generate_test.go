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
	Token string `json:"token"`
}

type InvoiceStatus string

type Invoice struct {
	ID        string            `json:"id"`
	Status    InvoiceStatus     `json:"status" jsonschema_enum:"draft,sent,paid"`
	Amount    int               `json:"amount"`
	Note      *string           `json:"note,omitempty"`
	Tags      []string          `json:"tags"`
	Metadata  map[string]string `json:"metadata"`
	CreatedAt time.Time         `json:"createdAt"`
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

	for _, name := range []string{"types.go", "client.go"} {
		if _, ok := files[name]; !ok {
			t.Fatalf("expected file %q in generated output", name)
		}
	}

	types := string(files["types.go"])
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
		`type BillingClient struct {`,
		`type ConnectedBillingClient struct {`,
		`type BillingClientBillingNamespace struct {`,
		`type BillingClientBillingInvoiceNamespace struct {`,
		`func (n *BillingClientBillingInvoiceNamespace) Get(ctx context.Context, req GetInvoiceRequest) (GetInvoiceResponse, error) {`,
		`client.Call[GetInvoiceResponse](ctx, n.conn, "billing.invoice.get", req)`,
		`func (n *BillingClientUserNamespace) Get(ctx context.Context, req UserGetRequest) (UserGetResponse, error) {`,
		`type ConnectedBillingClientEvents struct {`,
		`client.RegisterEvent[InvoiceUpdated](conn, "billing.invoice.updated")`,
		`func (c *ConnectedBillingClient) Close() error {`,
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

func TestMethodNamespaceLeafCollisionRejected(t *testing.T) {
	p := buildFixtureProtocol(t)
	p.Methods = append(p.Methods, protocol.Method{
		Name:         "billing.invoice",
		RequestType:  p.Methods[0].RequestType,
		ResponseType: p.Methods[0].ResponseType,
	})

	if _, err := Generate(p, Options{}); err == nil {
		t.Fatalf("expected method namespace collision (billing.invoice vs billing.invoice.get) to be rejected")
	}
}
