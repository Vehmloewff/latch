// Package golang generates an idiomatic, standalone Go client from
// Latchwire's protocol IR. Generated output never depends on the server
// implementation package, only on the small public runtime at
// github.com/vehmloewff/latchwire/client. Every file is passed through
// go/format before being returned, per spec section 50.
package golang

import (
	"fmt"
	"go/format"

	"github.com/vehmloewff/latchwire/codegen"
	"github.com/vehmloewff/latchwire/names"
	"github.com/vehmloewff/latchwire/protocol"
)

// Options configures Go client generation.
type Options struct {
	// Package is the generated package name. Defaults to
	// snake_case(protocol name) + "client" (no underscore, since Go package
	// names conventionally avoid them), or "latchwireclient" if the
	// protocol has no name.
	Package string

	// ClientName is the base name for the generated types: "<Name>Client"
	// and "Connected<Name>Client". Defaults to PascalCase(protocol name) +
	// "Client", or "LatchwireClient" if the protocol has no name.
	ClientName string
}

func (o Options) resolve(p *protocol.Protocol) Options {
	out := o
	base := "latchwire"
	if p.Name != "" {
		base = p.Name
	}
	if out.Package == "" {
		out.Package = names.CamelCase(base) + "client"
	}
	if out.ClientName == "" {
		out.ClientName = names.PascalCase(base) + "Client"
	}
	return out
}

// Generate renders a complete Go client package from p, returning a map of
// relative file path to file contents. It is deterministic: the same
// protocol always produces byte-identical output.
func Generate(p *protocol.Protocol, opts Options) (map[string][]byte, error) {
	opts = opts.resolve(p)

	typeNames, err := names.AssignTypeNames(p.Types)
	if err != nil {
		return nil, fmt.Errorf("golang: %w", err)
	}

	header := codegen.HeaderComment(p.Name, p.Version)

	typesSrc := header + generateTypesFile(opts.Package, p, typeNames)
	typesFormatted, err := format.Source([]byte(typesSrc))
	if err != nil {
		return nil, fmt.Errorf("golang: format types.go: %w\n%s", err, typesSrc)
	}

	clientBody, err := generateClientFile(opts.Package, p, opts.ClientName, typeNames)
	if err != nil {
		return nil, fmt.Errorf("golang: %w", err)
	}
	clientSrc := header + clientBody
	clientFormatted, err := format.Source([]byte(clientSrc))
	if err != nil {
		return nil, fmt.Errorf("golang: format client.go: %w\n%s", err, clientSrc)
	}

	return map[string][]byte{
		"types.go":  typesFormatted,
		"client.go": clientFormatted,
	}, nil
}
