package api

import (
	"bytes"
	"encoding/json"
	"github.com/google/jsonschema-go/jsonschema"
	"io"
	"olixops-k8s-ctl/internal/domain/fault"
	"reflect"
	"sync"
	"time"
)

const MaxJSONBytes int64 = 4 << 20
const ReadTimeout = 30 * time.Second
const ShellTimeout = 35 * time.Second

var inputSchemas sync.Map

type inputSchema struct {
	schema   *jsonschema.Schema
	resolved *jsonschema.Resolved
	err      error
}

func schemaFor[T any]() inputSchema {
	t := reflect.TypeFor[T]()
	if cached, ok := inputSchemas.Load(t); ok {
		return cached.(inputSchema)
	}
	s, e := jsonschema.For[T](nil)
	result := inputSchema{schema: s, err: e}
	if e == nil {
		result.resolved, result.err = s.Resolve(nil)
	}
	actual, _ := inputSchemas.LoadOrStore(t, result)
	return actual.(inputSchema)
}

// Decode uses the same schema inference and validation library as the MCP SDK.
// Business defaults and validation remain in the shared application use cases.
func Decode[In any](r io.Reader) (In, error) {
	var in In
	raw, err := io.ReadAll(io.LimitReader(r, MaxJSONBytes+1))
	if err != nil {
		return in, fault.Invalid("cannot read request body")
	}
	if int64(len(raw)) > MaxJSONBytes {
		return in, fault.Invalid("JSON request exceeds 4 MiB")
	}
	var object map[string]any
	if json.Unmarshal(raw, &object) != nil || object == nil {
		return in, fault.Invalid("exactly one JSON object required")
	}
	spec := schemaFor[In]()
	if spec.err != nil {
		return in, fault.Disabled("operation input schema unavailable")
	}
	for _, name := range spec.schema.Required {
		if _, ok := object[name]; !ok {
			return in, fault.Invalid("field %s is required", name)
		}
	}
	if err = spec.resolved.Validate(object); err != nil {
		return in, fault.Invalid("invalid input field, value type or required value")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&in); err != nil {
		return in, fault.Invalid("invalid input field or value type")
	}
	return in, nil
}
