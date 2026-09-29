package golang

import (
	"fmt"
	"go/format"
	"strings"

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

// Generate renders a complete Go client in one source file.
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
	typesSrc := generateTypesFile(opts.Package, p, typeNames)
	clientSrc, err := generateClientFile(opts.Package, p, opts.ClientName, typeNames)
	if err != nil {
		return nil, fmt.Errorf("golang: %w", err)
	}

	imports := "import (\n\t\"context\"\n\t\"sync\"\n\t\"time\"\n\n\t\"github.com/vehmloewff/latch/client\"\n)\n\n"
	source := header + fmt.Sprintf("package %s\n\n", opts.Package) + imports + goBody(typesSrc) + "\n\n" + goBody(clientSrc) + "\n"
	formatted, err := format.Source([]byte(source))
	if err != nil {
		return nil, fmt.Errorf("golang: format client.go: %w\n%s", err, source)
	}
	return map[string][]byte{"client.go": formatted}, nil
}

func goBody(src string) string {
	packageOffset := strings.Index(src, "package ")
	if packageOffset < 0 {
		return src
	}
	newline := strings.IndexByte(src[packageOffset:], '\n')
	if newline < 0 {
		return ""
	}
	body := strings.TrimLeft(src[packageOffset+newline+1:], "\n")
	if strings.HasPrefix(body, "import (") {
		end := strings.Index(body, ")\n\n")
		if end >= 0 {
			body = body[end+3:]
		}
	} else if strings.HasPrefix(body, "import ") {
		if end := strings.IndexByte(body, '\n'); end >= 0 {
			body = body[end+1:]
		}
	}
	return strings.TrimSpace(body)
}
