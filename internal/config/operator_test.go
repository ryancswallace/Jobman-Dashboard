package config

import (
	"path/filepath"
	"strings"
	"testing"
)

const operatorExample = `{"schemaVersion":1,"databaseURLFile":"/private/operator-db","deployments":[{"id":"11111111-1111-4111-8111-111111111111","name":"Research"}]}`

func TestOperatorConfigIsMinimalBoundedAndReadOnly(t *testing.T) {
	c, e := DecodeOperator(strings.NewReader(operatorExample))
	if e != nil || len(c.Deployments) != 1 || c.SchemaVersion != 1 {
		t.Fatal(c, e)
	}
	for _, value := range []string{`[]`, `[{"id":"11111111-1111-4111-8111-111111111111"}]`} {
		input := `{"schemaVersion":1,"databaseURLFile":"/private/operator-db","deployments":` + value + `}`
		if _, e := DecodeOperator(strings.NewReader(input)); e != nil {
			t.Fatal("minimal source set rejected", e)
		}
	}
	for name, input := range map[string]string{
		"zero schema":     strings.Replace(operatorExample, `"schemaVersion":1`, `"schemaVersion":0`, 1),
		"future schema":   strings.Replace(operatorExample, `"schemaVersion":1`, `"schemaVersion":2`, 1),
		"fraction schema": strings.Replace(operatorExample, `"schemaVersion":1`, `"schemaVersion":1.0`, 1),
		"relative DB":     strings.Replace(operatorExample, `/private/operator-db`, `relative`, 1),
		"noncanonical ID": strings.Replace(operatorExample, `11111111-1111-4111-8111-111111111111`, `not-a-uuid`, 1),
		"duplicate ID":    strings.Replace(operatorExample, `"name":"Research"}]`, `"name":"Research"},{"id":"11111111-1111-4111-8111-111111111111"}]`, 1),
		"long name":       strings.Replace(operatorExample, `Research`, strings.Repeat("a", 201), 1),
		"control name":    strings.Replace(operatorExample, `Research`, `line\nfeed`, 1),
		"duplicate field": strings.Replace(operatorExample, `{`, `{"schemaVersion":1,`, 1),
		"null set":        strings.Replace(operatorExample, `[{"id":"11111111-1111-4111-8111-111111111111","name":"Research"}]`, `null`, 1),
		"trailing":        operatorExample + `{}`,
		"too large":       strings.Repeat(" ", int(MaxConfigBytes)) + operatorExample,
	} {
		t.Run(name, func(t *testing.T) {
			if _, e := DecodeOperator(strings.NewReader(input)); e == nil {
				t.Fatal("invalid operator configuration accepted")
			}
		})
	}
	for _, field := range []string{"configurationRevision", "oidc", "encryption", "serverTLS", "controls", "webRoot", "listen", "notifications", "components"} {
		t.Run(field, func(t *testing.T) {
			input := `{"` + field + `":"PRIVATE-CANARY",` + operatorExample[1:]
			if _, e := DecodeOperator(strings.NewReader(input)); e == nil || strings.Contains(e.Error(), "PRIVATE-CANARY") {
				t.Fatalf("foreign runtime field admitted/leaked: %v", e)
			}
		})
	}
	c.Deployments = make([]OperatorDeployment, 33)
	if c.Validate() == nil {
		t.Fatal("unbounded deployment set accepted")
	}
}

func TestOperatorExampleLoadsWithoutRuntimeMaterial(t *testing.T) {
	path, err := filepath.Abs("../../deploy/operator.example.json")
	if err != nil {
		t.Fatal(err)
	}
	c, err := LoadOperator(path)
	if err != nil || len(c.Deployments) != 1 {
		t.Fatal("operator example rejected", err)
	}
}
