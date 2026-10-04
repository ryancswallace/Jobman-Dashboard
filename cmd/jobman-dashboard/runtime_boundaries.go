package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"strings"

	"github.com/ryancswallace/jobman-dashboard/internal/config"
)

// The static root stays open for the process lifetime. Root.FS refuses symlinks
// escaping that root, including links introduced after startup validation.
func openStatic(c config.Config) (*os.Root, error) {
	web, err := filepath.EvalSymlinks(c.WebRoot)
	if err != nil {
		return nil, errors.New("webRoot must contain the compiled web application")
	}
	before, err := os.Stat(web)
	if err != nil || !before.IsDir() {
		return nil, errors.New("webRoot must be a directory")
	}
	for field, path := range c.PrivateFilePaths() {
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil || config.ContainsPath(web, resolved) {
			return nil, fmt.Errorf("%s must resolve to an accessible private file outside webRoot", field)
		}
	}
	if c.Reports.ObjectRoot != "" {
		objects, err := filepath.EvalSymlinks(c.Reports.ObjectRoot)
		// Object storage may create its final directory; its parent must already
		// exist. Resolve that parent so absent roots cannot hide a web alias.
		if errors.Is(err, os.ErrNotExist) {
			parent, parentErr := filepath.EvalSymlinks(filepath.Dir(c.Reports.ObjectRoot))
			if parentErr == nil {
				objects, err = filepath.Join(parent, filepath.Base(c.Reports.ObjectRoot)), nil
			}
		}
		if err != nil || config.ContainsPath(web, objects) || config.ContainsPath(objects, web) {
			return nil, errors.New("reports.objectRoot must resolve outside and not contain webRoot")
		}
	}
	root, err := os.OpenRoot(web)
	if err != nil {
		return nil, errors.New("webRoot could not be opened")
	}
	opened, err := root.Stat(".")
	if err != nil || !os.SameFile(before, opened) {
		root.Close()
		return nil, errors.New("webRoot changed during startup")
	}
	index, err := root.Stat("index.html")
	if err != nil || !index.Mode().IsRegular() {
		root.Close()
		return nil, errors.New("webRoot has no confined compiled index.html")
	}
	return root, nil
}

// Go module versions are canonical full semantic versions, optionally with
// prerelease identifiers (which include Go pseudo-versions) or +incompatible.
// No dependency changes or external tooling are needed to validate build info.
var moduleVersion = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?(?:\+incompatible)?$`)

func companionBuildVersion(build *debug.BuildInfo) (string, error) {
	if build != nil {
		for _, dependency := range build.Deps {
			if dependency == nil || dependency.Path != "github.com/ryancswallace/jobman-diagnose" {
				continue
			}
			version := dependency.Version
			match := moduleVersion.FindStringSubmatch(version)
			if dependency.Replace != nil || len(version) > 256 || match == nil {
				break
			}
			for _, identifier := range strings.Split(match[4], ".") {
				if len(identifier) > 1 && identifier[0] == '0' && strings.Trim(identifier, "0123456789") == "" {
					return "", errors.New("diagnosis requires an immutable versioned companion dependency")
				}
			}
			return version, nil
		}
	}
	return "", errors.New("diagnosis requires an immutable versioned companion dependency")
}
