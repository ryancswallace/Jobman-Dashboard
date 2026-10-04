//go:build !unix

package reports

import "os"

func openObjectNoFollow(*os.Root, string) (*os.File, error)         { return nil, ErrObject }
func validateObjectAccess(*os.File, ObjectAccess, bool, bool) error { return ErrObject }
func prepareObjectFile(*os.File, ObjectAccess) error                { return ErrObject }
func publishObjectPermissions(*os.File, ObjectAccess) error         { return ErrObject }
