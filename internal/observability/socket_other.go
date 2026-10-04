//go:build !linux && !darwin

package observability

import "context"

type Listener struct{ Server *Server }

func Listen(context.Context, Options) (*Listener, error) { return nil, ErrUnavailable }
func (*Listener) Close()                                 {}
