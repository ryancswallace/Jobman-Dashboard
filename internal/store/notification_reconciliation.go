package store

import (
	"context"
	"slices"

	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
	"github.com/ryancswallace/jobman-dashboard/internal/notifications"
)

// NotificationReconciliationCandidates uses only the indexed current scope
// selections, never historical participation or a supplied arbitrary principal.
// Explicit namespace-wide intent is preferred; at most32 current aliases/rules
// are returned. Source authorization is performed separately outside this query.
func (s *Store) NotificationReconciliationCandidates(ctx context.Context, deployment, namespace string) ([]notifications.ReconciliationCandidate, error) {
	if !eventUUID(deployment) || !eventUUID(namespace) {
		return nil, notifications.ErrInvalid
	}
	rows, err := s.Pool.Query(ctx, `SELECT a.id::text,a.display_name,a.directory_id::text,i.issuer,i.subject,r.id::text,r.revision,r.payload
 FROM dashboard_notification_rule_scopes n JOIN dashboard_notification_rules r ON r.id=n.rule_id AND r.account_id=n.account_id
 JOIN dashboard_accounts a ON a.id=r.account_id JOIN dashboard_identity_aliases i ON i.account_id=a.id
 WHERE n.deployment_id=$1::uuid AND n.namespace_id=$2::uuid AND r.enabled AND r.deleted_at IS NULL AND a.disabled_at IS NULL
 ORDER BY a.id,r.id,i.issuer,i.subject LIMIT 32`, deployment, namespace)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []notifications.ReconciliationCandidate{}
	for rows.Next() {
		var c notifications.ReconciliationCandidate
		var id string
		var revision int64
		var data []byte
		if err = rows.Scan(&c.Actor.Account.ID, &c.Actor.Account.DisplayName, &c.Actor.DirectoryID, &c.Actor.Issuer, &c.Actor.Subject, &id, &revision, &data); err != nil {
			return nil, err
		}
		c.Rule, err = decodeNotificationRule(data, id, c.Actor.Account.ID, revision)
		if err != nil {
			return nil, err
		}
		c.Namespace = notifications.NamespaceRef{DeploymentID: deployment, NamespaceID: namespace}
		if !slices.Contains(c.Rule.Namespaces, c.Namespace) {
			return nil, notifications.ErrInvalid
		}
		result = append(result, c)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	slices.SortStableFunc(result, func(a, b notifications.ReconciliationCandidate) int {
		if a.Rule.Scope == notifications.ScopeNamespaceJobs && b.Rule.Scope != notifications.ScopeNamespaceJobs {
			return -1
		}
		if b.Rule.Scope == notifications.ScopeNamespaceJobs && a.Rule.Scope != notifications.ScopeNamespaceJobs {
			return 1
		}
		return 0
	})
	return result, nil
}
func (s *Store) VerifyNotificationReconciliationCandidate(ctx context.Context, c notifications.ReconciliationCandidate) error {
	current, err := s.NotificationRule(ctx, c.Actor, c.Rule.ID)
	if err != nil {
		return err
	}
	if !current.Enabled || current.Revision != c.Rule.Revision || !slices.Contains(current.Namespaces, c.Namespace) {
		return notifications.ErrConflict
	}
	revoked, err := s.NotificationActivationRevocations(ctx, c.Actor, current.ID, current.Revision)
	if err != nil {
		return err
	}
	for _, a := range current.Activation {
		if a.Namespace() == c.Namespace {
			if slices.Contains(revoked, a.ID) || a.Status == notifications.ActivationInaccessible || a.Status == notifications.ActivationDisabled {
				return monitoring.ErrForbidden
			}
			return nil
		}
	}
	return monitoring.ErrForbidden
}
