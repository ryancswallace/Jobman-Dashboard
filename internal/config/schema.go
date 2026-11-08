package config

import (
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
)

// WriteSchema exports the decoder's structural contract. Native static validation
// remains authoritative for cross-field, security and operational constraints.
func WriteSchema(writer io.Writer, role string) error {
	var document any
	switch role {
	case "serve", "api":
		document = Config{}
	case "worker":
		document = WorkerConfig{}
	case "broker":
		document = BrokerConfig{}
	case "operator":
		document = OperatorConfig{}
	default:
		return errors.New("unsupported configuration schema role")
	}
	schema := structuralSchema(reflect.TypeOf(document))
	schema["$schema"] = "https://json-schema.org/draft/2020-12/schema"
	schema["$id"] = "urn:jobman:dashboard:configuration:" + role + ":1"
	schema["description"] = "Structural schema only. Use validate-config for authoritative offline validation; configurationRevision is a separate identity fence."
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	return encoder.Encode(schema)
}

func structuralSchema(value reflect.Type) map[string]any {
	if value.Kind() == reflect.Pointer {
		return structuralSchema(value.Elem())
	}
	switch value.Kind() {
	case reflect.Struct:
		properties := make(map[string]any)
		for index := 0; index < value.NumField(); index++ {
			field := value.Field(index)
			name := strings.Split(field.Tag.Get("json"), ",")[0]
			if name != "" && name != "-" && field.IsExported() {
				properties[name] = structuralSchema(field.Type)
			}
		}
		return map[string]any{"type": "object", "properties": properties, "additionalProperties": false}
	case reflect.Slice, reflect.Array:
		return map[string]any{"type": "array", "items": structuralSchema(value.Elem())}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint32, reflect.Uint64:
		return map[string]any{"type": "integer"}
	default:
		return map[string]any{"type": "string"}
	}
}
