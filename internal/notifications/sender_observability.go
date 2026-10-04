package notifications

import "github.com/ryancswallace/jobman-dashboard/internal/observability"

// SetObserver is called only before Run/Step; it does not modify delivery gates.
func (s *Sender) SetObserver(r *observability.Registry) { s.observer = r }
func providerObservation(outcome string, err error) string {
	if err != nil {
		return "unavailable"
	}
	switch outcome {
	case "accepted", "retry", "unregistered", "invalid_token", "provider_error":
		return outcome
	default:
		return "failed"
	}
}
