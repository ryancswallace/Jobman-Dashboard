package auth

import "errors"

// DelegationMode describes the trusted process making a represented-user read.
// It is constructor configuration, never a client-selected request parameter.
// Service identity, certificate, operation and current user grants remain the
// authority boundary; this claim does not confer additional permissions.
type DelegationMode string

const (
	DelegationInteractive DelegationMode = "interactive"
	DelegationWorker      DelegationMode = "worker"
)

// Canonical preserves the existing interactive adapter default while allowing
// worker constructors to explicitly identify background requests. Unknown values
// fail closed; they never fall back to interactive or enter a signed assertion.
func (m DelegationMode) Canonical() (DelegationMode, error) {
	switch m {
	case "", DelegationInteractive:
		return DelegationInteractive, nil
	case DelegationWorker:
		return DelegationWorker, nil
	default:
		return "", errors.New("invalid configured delegation mode")
	}
}
