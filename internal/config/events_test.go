package config

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestEventIngestionRequiresExplicitBooleanConfiguration(t *testing.T) {
	c := example(t)
	c.Events.Enabled = true
	data := encoded(t, c)
	got, err := Decode(bytes.NewReader(data))
	if err != nil || !got.Events.Enabled {
		t.Fatal("explicit event enablement rejected", err)
	}
	for _, value := range []string{`"true"`, `1`, `null`, `[]`, `{}`} {
		changed := strings.Replace(string(data), `"enabled":true`, `"enabled":`+value, 1)
		if _, err = Decode(strings.NewReader(changed)); err == nil {
			t.Fatal("ambiguous event enablement accepted", value)
		}
	}
	eventConfig, err := json.Marshal(c.Events)
	if err != nil {
		t.Fatal(err)
	}
	without := strings.Replace(string(data), `"events":`+string(eventConfig), `"events":{}`, 1)
	got, err = Decode(strings.NewReader(without))
	if err != nil || got.Events.Enabled {
		t.Fatal("absent enablement starts workers", err)
	}
}
