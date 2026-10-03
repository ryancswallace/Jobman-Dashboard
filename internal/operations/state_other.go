//go:build !linux && !darwin

package operations

import (
	"errors"
	"os"
)

func openStateFile(*os.Root, string, int) (*os.File, error) {
	return nil, errors.New("broker state requires Linux or macOS")
}
func lockState(*os.File) error { return errors.New("broker state requires Linux or macOS") }
