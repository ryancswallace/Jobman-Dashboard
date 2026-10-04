package config

import (
	"bytes"
	"strings"
	"testing"
)

func TestObservabilityStrictOptionalConfiguration(t *testing.T) {
	c := example(t)
	c.Observability = &Observability{SocketPath: "/run/jobman-api/operator.sock", OperatorConfigFile: "/etc/jobman-dashboard/operator.json"}
	raw := encoded(t, c)
	if _, err := Decode(bytes.NewReader(raw)); err != nil {
		t.Fatal(err)
	}
	for _, replacement := range []string{`"socketPath":"relative"`, `"socketPath":null`, `"socketPath":32`, `"socketPath":"/run/../tmp/sock"`, `"socketPath":"/x.sock"`, `"socketPath":"/run/jobman-api/operator.sock","unknown":true`} {
		bad := strings.Replace(string(raw), `"socketPath":"/run/jobman-api/operator.sock"`, replacement, 1)
		if _, err := Decode(strings.NewReader(bad)); err == nil {
			t.Fatal("accepted invalid observation shape", replacement)
		}
	}
	c.Observability.OperatorConfigFile = c.WebRoot + "/operator.json"
	if c.Validate() == nil {
		t.Fatal("public operator config accepted")
	}
}
