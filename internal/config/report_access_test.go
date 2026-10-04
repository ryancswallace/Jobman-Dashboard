package config

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestExplicitReportAccessProfileIsStrictAndRequiresStorage(t *testing.T) {
	c := example(t)
	c.Reports = Reports{ObjectRoot: "/private/report-objects", ObjectAccess: &ReportObjectAccess{Mode: "shared_group", WorkerUID: 21910, ReaderGID: 21911}}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decode(strings.NewReader(string(encoded))); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{`{}`, `{"mode":"shared_group","workerUid":21910}`, `{"mode":"shared_group","workerUid":0,"readerGid":21911}`, `{"mode":"shared_group","workerUid":21910,"readerGid":0}`, `{"mode":"shared_group","workerUid":4294967295,"readerGid":21911}`, `{"mode":"owner_only","workerUid":21910,"readerGid":21911}`, `{"mode":"shared_group","workerUid":21910,"readerGid":21911,"allowAcl":true}`} {
		var raw map[string]any
		if json.Unmarshal(encoded, &raw) != nil {
			t.Fatal("decode test input")
		}
		var profile map[string]any
		if json.Unmarshal([]byte(value), &profile) != nil {
			t.Fatal("decode test profile")
		}
		raw["reports"].(map[string]any)["objectAccess"] = profile
		bad, _ := json.Marshal(raw)
		if _, err := Decode(strings.NewReader(string(bad))); err == nil {
			t.Fatal("unsafe access profile accepted", value)
		}
	}
	c.Reports.ObjectRoot = ""
	if c.Validate() == nil {
		t.Fatal("access profile without storage accepted")
	}
	c.Reports = Reports{}
	if err := c.Validate(); err != nil {
		t.Fatal("default owner-only config changed", err)
	}
}

func TestReportAccessNumericShapeIsStrictForAPIAndWorker(t *testing.T) {
	profile := Reports{ObjectRoot: "/private/report-objects", ObjectAccess: &ReportObjectAccess{Mode: "shared_group", WorkerUID: 21910, ReaderGID: 21911}}
	api := example(t)
	api.Reports = profile
	worker := WorkerConfig{ConfigurationRevision: 1, DatabaseURLFile: "/private/worker-db", Components: []WorkerComponent{WorkerRetention}, Controls: []Control{}, Reports: profile}
	for _, test := range []struct {
		name   string
		value  any
		decode func(string) error
	}{
		{"api", api, func(value string) error { _, err := Decode(strings.NewReader(value)); return err }},
		{"worker", worker, func(value string) error { _, err := DecodeWorker(strings.NewReader(value)); return err }},
	} {
		t.Run(test.name, func(t *testing.T) {
			data, err := json.Marshal(test.value)
			if err != nil {
				t.Fatal(err)
			}
			if err := test.decode(string(data)); err != nil {
				t.Fatal("valid profile rejected", err)
			}
			for _, number := range []string{"-1", "21910.0", "21910.5", "2.191e4", "4294967296", "2147483648", "0", "null", `"21910"`, "true"} {
				bad := strings.Replace(string(data), `"workerUid":21910`, `"workerUid":`+number, 1)
				if bad == string(data) {
					t.Fatal("numeric fixture not replaced")
				}
				if err := test.decode(bad); err == nil {
					t.Fatal("invalid worker UID accepted", number)
				}
			}
			bad := strings.Replace(string(data), `"objectAccess":{"mode":"shared_group","workerUid":21910,"readerGid":21911}`, `"objectAccess":null`, 1)
			if bad == string(data) || test.decode(bad) == nil {
				t.Fatal("null access profile accepted")
			}
		})
	}
}
