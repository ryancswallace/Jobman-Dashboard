// Package buildinfo describes the executable without opening configuration,
// credentials, the database, or network connections.
package buildinfo

import (
	"encoding/json"
	"io"
	"runtime"
	"runtime/debug"
)

// Release tooling sets these from a committed source archive with -X. Ordinary
// development builds retain Go's available VCS metadata and an explicit dev name.
var (
	Version  = "development"
	Revision = ""
	BuiltAt  = ""
)

type Info struct {
	FormatVersion int               `json:"formatVersion"`
	Version       string            `json:"version"`
	Revision      string            `json:"revision,omitempty"`
	BuiltAt       string            `json:"builtAt,omitempty"`
	Modified      bool              `json:"modified"`
	GoVersion     string            `json:"goVersion"`
	OS            string            `json:"os"`
	Architecture  string            `json:"architecture"`
	Dependencies  map[string]string `json:"dependencies"`
}

func Snapshot() Info {
	i := Info{FormatVersion: 1, Version: Version, Revision: Revision, BuiltAt: BuiltAt,
		GoVersion: runtime.Version(), OS: runtime.GOOS, Architecture: runtime.GOARCH,
		Dependencies: map[string]string{}}
	if b, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range b.Settings {
			switch setting.Key {
			case "vcs.revision":
				if i.Revision == "" {
					i.Revision = setting.Value
				}
			case "vcs.modified":
				i.Modified = setting.Value == "true"
			}
		}
		for _, module := range b.Deps {
			// Never print replacement paths: a developer may use a local checkout.
			if module.Replace != nil {
				i.Dependencies[module.Path] = "replaced development dependency"
			} else {
				i.Dependencies[module.Path] = module.Version
			}
		}
	}
	return i
}

func Write(w io.Writer) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(Snapshot())
}
