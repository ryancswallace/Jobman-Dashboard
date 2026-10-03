//go:build !linux && !darwin

package logs

import (
	"errors"
	"os"
)

func openRelative(_, _ string) (*os.File, error) {
	return nil, errors.New("log broker requires Linux or macOS safe-open support")
}
