package typescript

import (
	"bytes"
	"reflect"
	"testing"

	"github.com/vehmloewff/latchwire/protocol"
	"github.com/vehmloewff/latchwire/reflectapi"
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

	for _, name := range []string{"runtime.ts", "types.ts", "client.ts", "index.ts"} {
		if _, ok := files[name]; !ok {
			t.Fatalf("expected file %q in generated output", name)
		}
	}

	types := string(files["types.ts"])
	wantSnippets := []string{
		`export interface Invoice {`,
		`amount: number;`,
		`note?: string | null;`,
		`tags: string[];`,
		`metadata: Record<string, string>;`,
		`export type InvoiceStatus =`,
		`"draft"`,
		`"sent"`,
		`"paid"`,
	}
	for _, want := range wantSnippets {
		if !bytes.Contains([]byte(types), []byte(want)) {
			t.Errorf("types.ts missing expected snippet %q; got:\n%s", want, types)
		}
	}

	client := string(files["client.ts"])
	wantClientSnippets := []string{
		`readonly billing = {`,
		`invoice: {`,
		`get: (req: GetInvoiceRequest): Promise<GetInvoiceResponse> => this.call("billing.invoice.get", req),`,
		`readonly user = {`,
		`get: (req: UserGetRequest): Promise<UserGetResponse> => this.call("user.get", req),`,
		`readonly events = {`,
		`billingInvoiceUpdated: new EventStream<InvoiceUpdated>(),`,
		`case "billing.invoice.updated":`,
		`export class BillingClient {`,
		`export class ConnectedBillingClient extends BaseConnection {`,
	}
	for _, want := range wantClientSnippets {
		if !bytes.Contains([]byte(client), []byte(want)) {
			t.Errorf("client.ts missing expected snippet %q; got:\n%s", want, client)
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

func TestEventPropertyCollisionRejected(t *testing.T) {
	p := buildFixtureProtocol(t)
	p.Events = append(p.Events, protocol.Event{
		Name:        "billingInvoiceUpdated", // camelCases to the same property as "billing.invoice.updated"
		PayloadType: p.Events[0].PayloadType,
	})

	if _, err := Generate(p, Options{}); err == nil {
		t.Fatalf("expected event property name collision to be rejected")
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
