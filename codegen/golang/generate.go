// Package golang generates an idiomatic, standalone Go client from
// Latch's protocol IR. Generated output never depends on the server
// implementation package, only on the small public runtime at
// github.com/vehmloewff/latch/client. Every file is passed through
// go/format before being returned, per spec section 50.
package golang

import (
	"fmt"
	"go/format"

	"github.com/vehmloewff/latch/codegen"
	"github.com/vehmloewff/latch/names"
	"github.com/vehmloewff/latch/protocol"
)

// Options configures Go client generation.
type Options struct {
	// Package is the generated package name. Defaults to "latchclient".
	Package string

	// ClientName is the base name for the generated types: "<Name>Client"
	// and "Connected<Name>Client". Defaults to "LatchClient".
	ClientName string
}

func (o Options) resolve(_ *protocol.Protocol) Options {
	out := o
	base := "latch"
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
	if err := names.ValidateIdentifiers(methodNames(p)); err != nil {
		return nil, fmt.Errorf("golang: %w", err)
	}
	opts = opts.resolve(p)

	typeNames, err := names.AssignTypeNames(p.Types)
	if err != nil {
		return nil, fmt.Errorf("golang: %w", err)
	}

	header := codegen.HeaderComment(p.Version)

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
