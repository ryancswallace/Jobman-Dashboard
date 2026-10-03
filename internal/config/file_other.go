//go:build !unix

package config

import (
	"errors"
	"os"
)

func openNoFollow(string) (*os.File, error) {
	return nil, errors.New("secure file loading requires a supported Unix host")
}
