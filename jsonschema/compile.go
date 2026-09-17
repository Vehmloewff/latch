package jsonschema

import (
	"bytes"
	"encoding/json"
	"fmt"

	validator "github.com/santhosh-tekuri/jsonschema/v6"
)

// Validator wraps a compiled JSON Schema so it can be reused across many
// requests without recompiling. Schemas are compiled exactly once, during
// protocol finalization.
type Validator struct {
	schema *validator.Schema
}

// Compile compiles a JSON Schema document (as produced by BuildDocument)
// under a synthetic, unique resource URL. It is safe to call many times with
// different documents.
func Compile(resourceURL string, doc map[string]any) (*Validator, error) {
	raw, err := json.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("marshal schema document: %w", err)
	}

	parsed, err := validator.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("parse schema document: %w", err)
	}

	c := validator.NewCompiler()
	if err := c.AddResource(resourceURL, parsed); err != nil {
		return nil, fmt.Errorf("add schema resource: %w", err)
	}

	sch, err := c.Compile(resourceURL)
	if err != nil {
		return nil, fmt.Errorf("compile schema: %w", err)
	}

	return &Validator{schema: sch}, nil
}

// ValidateJSON validates raw JSON bytes against the compiled schema. It
// returns a descriptive error (never a panic) for both malformed JSON and
// schema violations.
func (v *Validator) ValidateJSON(raw []byte) error {
	inst, err := validator.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	if err := v.schema.Validate(inst); err != nil {
		return err
	}
	return nil
}
