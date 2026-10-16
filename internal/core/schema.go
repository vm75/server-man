package core

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"slices"
	"unicode/utf8"
)

// Field is the small subset of JSON Schema needed by management operations.
// Keeping a single definition makes HTTP and MCP validation identical.
type Field struct {
	Type        string   `json:"type"`
	Description string   `json:"description,omitempty"`
	Enum        []string `json:"enum,omitempty"`
	MinLength   *int     `json:"minLength,omitempty"`
	MaxLength   *int     `json:"maxLength,omitempty"`
	Minimum     *float64 `json:"minimum,omitempty"`
	Maximum     *float64 `json:"maximum,omitempty"`
	Required    bool     `json:"-"`
}

func StringField(description string, required bool, maxLength int) Field {
	field := Field{Type: "string", Description: description, Required: required, MaxLength: &maxLength}
	if required {
		minimum := 1
		field.MinLength = &minimum
	}
	return field
}

func EnumField(description string, required bool, values ...string) Field {
	return Field{Type: "string", Description: description, Required: required, Enum: values}
}

type Input map[string]any

func (i Input) String(name string) string  { value, _ := i[name].(string); return value }
func (i Input) Bool(name string) bool      { value, _ := i[name].(bool); return value }
func (i Input) Number(name string) float64 { value, _ := i[name].(float64); return value }

func schema(fields map[string]Field) map[string]any {
	required := []string{}
	for key, field := range fields {
		if field.Required {
			required = append(required, key)
		}
	}
	slices.Sort(required)
	return map[string]any{"type": "object", "properties": fields, "required": required, "additionalProperties": false}
}

func validate(raw json.RawMessage, fields map[string]Field) (Input, error) {
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	if len(raw) > 1<<20 {
		return nil, Invalid("arguments exceed 1 MiB")
	}
	if data := bytes.TrimSpace(raw); len(data) == 0 || data[0] != '{' {
		return nil, Invalid("arguments must be an object")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	var input Input
	if err := dec.Decode(&input); err != nil {
		return nil, Invalid("invalid JSON arguments")
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return nil, Invalid("expected one arguments object")
	}
	for key, value := range input {
		field, ok := fields[key]
		if !ok {
			return nil, Invalid("unknown argument %q", key)
		}
		switch field.Type {
		case "string":
			text, ok := value.(string)
			if !ok {
				return nil, Invalid("%s must be a string", key)
			}
			length := utf8.RuneCountInString(text)
			if field.MinLength != nil && length < *field.MinLength {
				return nil, Invalid("%s is too short", key)
			}
			if field.MaxLength != nil && length > *field.MaxLength {
				return nil, Invalid("%s is too long", key)
			}
			if len(field.Enum) > 0 && !slices.Contains(field.Enum, text) {
				return nil, Invalid("unsupported %s", key)
			}
		case "boolean":
			if _, ok := value.(bool); !ok {
				return nil, Invalid("%s must be a boolean", key)
			}
		case "integer", "number":
			num, ok := value.(float64)
			if !ok || math.IsNaN(num) || math.IsInf(num, 0) || (field.Type == "integer" && math.Trunc(num) != num) {
				return nil, Invalid("%s must be %s", key, field.Type)
			}
			if field.Minimum != nil && num < *field.Minimum {
				return nil, Invalid("%s is below its minimum", key)
			}
			if field.Maximum != nil && num > *field.Maximum {
				return nil, Invalid("%s exceeds its maximum", key)
			}
		default:
			return nil, fmt.Errorf("unsupported registered field type %q", field.Type)
		}
	}
	for key, field := range fields {
		if _, ok := input[key]; field.Required && !ok {
			return nil, Invalid("missing argument %q", key)
		}
	}
	return input, nil
}
