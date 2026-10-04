package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/ryancswallace/jobman-dashboard/internal/events"
	"github.com/ryancswallace/jobman-dashboard/internal/monitoring"
	"github.com/ryancswallace/jobman-dashboard/internal/notifications"
)

const (
	// Retention retires only expired, unreferenced snapshots; delayed evaluation
	// and current intervals preserve their original authority history.
	// Normal mutations stop at 1,000 retained snapshots. Up to 100 live rules
	// may then each disable and delete using the 200-record safety reserve.
	// Stops bypass the rate limit, but cannot reopen an interval or rule slot
	// without passing normal admission again. Hard retained maximum: 1,200.
	MaximumNotificationVersions           = 1000
	MaximumNotificationStopVersions       = 200
	MaximumNotificationMutationsPerMinute = 10
)

// Every operation binds the immutable account and directory identity to the
// caller's currently verified alias. An alias saved during creation is never
// required. Lock order is account, alias, rule, then canonical source IDs.
func lockNotificationOwner(ctx context.Context, tx pgx.Tx, actor monitoring.Actor, write bool) error {
	if !eventUUID(actor.Account.ID) || !eventUUID(actor.DirectoryID) || actor.Issuer == "" || len(actor.Issuer) > 512 || actor.Subject == "" || len(actor.Subject) > 512 {
		return monitoring.ErrForbidden
	}
	lock := " FOR SHARE"
	if write {
		lock = " FOR UPDATE"
	}
	var id string
	err := tx.QueryRow(ctx, `SELECT id::text FROM dashboard_accounts WHERE id=$1::uuid AND directory_id=$2::uuid AND disabled_at IS NULL`+lock, actor.Account.ID, actor.DirectoryID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return monitoring.ErrForbidden
	}
	if err != nil {
		return err
	}
	err = tx.QueryRow(ctx, `SELECT account_id::text FROM dashboard_identity_aliases WHERE account_id=$1::uuid AND issuer=$2 AND subject=$3 FOR KEY SHARE`, actor.Account.ID, actor.Issuer, actor.Subject).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return monitoring.ErrForbidden
	}
	return err
}

func decodeNotificationRule(data []byte, id, account string, revision int64) (notifications.Rule, error) {
	var rule notifications.Rule
	if len(data) == 0 || len(data) > notifications.MaximumRuleBytes || json.Unmarshal(data, &rule) != nil || rule.Validate() != nil || rule.ID != id || rule.AccountID != account || rule.Revision != revision {
		return notifications.Rule{}, notifications.ErrInvalid
	}
	return rule, nil
}

func readNotificationRule(ctx context.Context, tx pgx.Tx, account, id string) (notifications.Rule, error) {
	if !eventUUID(id) {
		return notifications.Rule{}, notifications.ErrNotFound
	}
	var data []byte
	var revision int64
	err := tx.QueryRow(ctx, `SELECT payload,revision FROM dashboard_notification_rules WHERE id=$1::uuid AND account_id=$2::uuid AND deleted_at IS NULL`, id, account).Scan(&data, &revision)
	if errors.Is(err, pgx.ErrNoRows) {
		return notifications.Rule{}, notifications.ErrNotFound
	}
	if err != nil {
		return notifications.Rule{}, err
	}
	return decodeNotificationRule(data, id, account, revision)
}

// NotificationRule returns a private persistence model. The service must apply
// current source authorization and a public projection before sending it out.
func (s *Store) NotificationRule(ctx context.Context, actor monitoring.Actor, id string) (notifications.Rule, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return notifications.Rule{}, err
	}
	defer tx.Rollback(ctx)
	if err = lockNotificationOwner(ctx, tx, actor, false); err != nil {
		return notifications.Rule{}, err
	}
	rule, err := readNotificationRule(ctx, tx, actor.Account.ID, id)
	if err != nil {
		return notifications.Rule{}, err
	}
	return rule, tx.Commit(ctx)
}

func (s *Store) ListNotificationRules(ctx context.Context, actor monitoring.Actor) ([]notifications.Rule, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if err = lockNotificationOwner(ctx, tx, actor, false); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT id::text,revision,payload FROM dashboard_notification_rules WHERE account_id=$1::uuid AND deleted_at IS NULL ORDER BY created_at,id LIMIT $2`, actor.Account.ID, notifications.MaximumRulesPerAccount+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	rules := []notifications.Rule{}
	for rows.Next() {
		var id string
		var revision int64
		var data []byte
		if err = rows.Scan(&id, &revision, &data); err != nil {
			return nil, err
		}
		rule, err := decodeNotificationRule(data, id, actor.Account.ID, revision)
		if err != nil {
			return nil, err
		}
		rules = append(rules, rule)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if len(rules) > notifications.MaximumRulesPerAccount {
		return nil, notifications.ErrCapacity
	}
	return rules, tx.Commit(ctx)
}

func notificationAdmission(ctx context.Context, tx pgx.Tx, account string, create, stop bool) error {
	var versions, recent, live int
	err := tx.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE regular_mutation AND recorded_at>clock_timestamp()-interval '1 minute') FROM dashboard_notification_rule_versions WHERE account_id=$1::uuid`, account).Scan(&versions, &recent)
	if err != nil {
		return err
	}
	maximum := MaximumNotificationVersions
	if stop {
		maximum += MaximumNotificationStopVersions
	}
	if versions >= maximum {
		return notifications.ErrCapacity
	}
	if !stop && recent >= MaximumNotificationMutationsPerMinute {
		return notifications.ErrRateLimited
	}
	if create {
		if err = tx.QueryRow(ctx, `SELECT count(*) FROM dashboard_notification_rules WHERE account_id=$1::uuid AND deleted_at IS NULL`, account).Scan(&live); err != nil {
			return err
		}
		if live >= notifications.MaximumRulesPerAccount {
			return notifications.ErrCapacity
		}
	}
	return nil
}

// Only newly installed active intervals need a current feed epoch check.
// Preserved immutable intervals may precede an explicitly reconciled recovery.
func checkNotificationFeeds(ctx context.Context, tx pgx.Tx, previous *notifications.Rule, next notifications.Rule) error {
	preserved := map[string]bool{}
	if previous != nil {
		for _, activation := range previous.Activation {
			preserved[activation.ID] = true
		}
	}
	var deployment string
	var checkpoint events.Checkpoint
	var namespaces []string
	for _, activation := range next.Activation {
		if activation.Status != notifications.ActivationActive || preserved[activation.ID] {
			continue
		}
		// Rule.Validate requires source/namespace sorted activation order.
		if deployment != activation.DeploymentID {
			var data []byte
			var status string
			err := tx.QueryRow(ctx, `SELECT status,namespace_ids::text[],checkpoint FROM dashboard_event_feeds WHERE deployment_id=$1::uuid FOR SHARE`, activation.DeploymentID).Scan(&status, &namespaces, &data)
			if errors.Is(err, pgx.ErrNoRows) {
				return notifications.ErrInactiveFeed
			}
			if err != nil {
				return err
			}
			checkpoint = events.Checkpoint{}
			if status != "active" || json.Unmarshal(data, &checkpoint) != nil || checkpoint.Validate() != nil || checkpoint.DeploymentID != activation.DeploymentID || !slices.Equal(namespaces, checkpoint.NamespaceIDs) {
				return notifications.ErrInactiveFeed
			}
			deployment = activation.DeploymentID
		}
		if !slices.Contains(namespaces, activation.NamespaceID) || checkpoint.ControlInstanceID != activation.Boundary.ControlInstanceID || checkpoint.RecoveryEpoch != activation.Boundary.RecoveryEpoch {
			return notifications.ErrInactiveFeed
		}
	}
	return nil
}

func persistNotificationRule(ctx context.Context, tx pgx.Tx, previous *notifications.Rule, next notifications.Rule, stop bool, action string) error {
	if err := next.Validate(); err != nil {
		return err
	}
	if previous != nil {
		if err := notifications.ValidateTransition(*previous, next); err != nil {
			return err
		}
	}
	if err := checkNotificationFeeds(ctx, tx, previous, next); err != nil {
		return err
	}
	data, err := json.Marshal(next)
	if err != nil {
		return err
	}
	if previous == nil {
		_, err = tx.Exec(ctx, `INSERT INTO dashboard_notification_rules(id,account_id,revision,enabled,payload,created_at,updated_at,deleted_at) VALUES($1::uuid,$2::uuid,$3,$4,$5,$6,$7,$8)`, next.ID, next.AccountID, next.Revision, next.Enabled, data, next.CreatedAt, next.UpdatedAt, next.DeletedAt)
	} else {
		tag, updateErr := tx.Exec(ctx, `UPDATE dashboard_notification_rules SET revision=$3,enabled=$4,payload=$5,updated_at=$6,deleted_at=$7 WHERE id=$1::uuid AND account_id=$2::uuid AND revision=$8 AND deleted_at IS NULL`, next.ID, next.AccountID, next.Revision, next.Enabled, data, next.UpdatedAt, next.DeletedAt, previous.Revision)
		err = updateErr
		if updateErr == nil && tag.RowsAffected() != 1 {
			return notifications.ErrConflict
		}
	}
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO dashboard_notification_rule_versions(rule_id,revision,account_id,payload,regular_mutation) VALUES($1::uuid,$2,$3::uuid,$4,$5)`, next.ID, next.Revision, next.AccountID, data, !stop)
	if err != nil {
		return err
	}
	preserved := map[string]bool{}
	if previous != nil {
		for _, activation := range previous.Activation {
			preserved[activation.ID] = true
		}
	}
	for _, activation := range next.Activation {
		if preserved[activation.ID] {
			continue
		}
		encoded, err := json.Marshal(activation)
		if err != nil || len(encoded) > 4096 {
			return notifications.ErrInvalid
		}
		tag, err := tx.Exec(ctx, `INSERT INTO dashboard_notification_activations(id,rule_id,created_revision,deployment_id,namespace_id,payload) VALUES($1::uuid,$2::uuid,$3,$4::uuid,$5::uuid,$6) ON CONFLICT DO NOTHING`, activation.ID, next.ID, next.Revision, activation.DeploymentID, activation.NamespaceID, encoded)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return notifications.ErrTransition
		}
	}
	// Every retained snapshot keeps indexed references to all of its intervals.
	// The retention worker cannot retire an interval payload behind any snapshot.
	historyIDs := make([]string, len(next.Activation))
	for i, a := range next.Activation {
		historyIDs[i] = a.ID
	}
	if _, err = tx.Exec(ctx, `INSERT INTO dashboard_notification_version_activations(rule_id,revision,activation_id) SELECT $1::uuid,$2,unnest($3::uuid[])`, next.ID, next.Revision, historyIDs); err != nil {
		return err
	}
	// Keep only current source-qualified selections in the candidate index.
	// This work is bounded by the validated320 scopes and remains in the same
	// transaction as the revision/snapshot/activation publication.
	deployments, namespaces, activationIDs := []string{}, []string{}, []string{}
	if next.DeletedAt == nil {
		for _, a := range next.Activation {
			deployments = append(deployments, a.DeploymentID)
			namespaces = append(namespaces, a.NamespaceID)
			activationIDs = append(activationIDs, a.ID)
		}
	}
	if _, err = tx.Exec(ctx, `DELETE FROM dashboard_notification_rule_scopes WHERE rule_id=$1::uuid AND NOT(activation_id=ANY($2::uuid[]))`, next.ID, activationIDs); err != nil {
		return err
	}
	if len(activationIDs) > 0 {
		_, err = tx.Exec(ctx, `INSERT INTO dashboard_notification_rule_scopes(rule_id,account_id,deployment_id,namespace_id,activation_id)
 SELECT $1::uuid,$2::uuid,deployment,namespace,activation FROM unnest($3::uuid[],$4::uuid[],$5::uuid[]) AS selected(deployment,namespace,activation)
 ON CONFLICT(rule_id,deployment_id,namespace_id) DO UPDATE SET activation_id=excluded.activation_id WHERE dashboard_notification_rule_scopes.activation_id<>excluded.activation_id`, next.ID, next.AccountID, deployments, namespaces, activationIDs)
		if err != nil {
			return err
		}
	}
	if err = syncNotificationActivationWork(ctx, tx, next); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO dashboard_audit(account_id,action,resource_kind,resource_id) VALUES($1::uuid,$2,'notification_rule',$3)`, next.AccountID, action, next.ID)
	return err
}

// CreateNotificationRule is called only after the service verifies current
// represented-user source access. Activations are server constructed, never
// copied from client input. Pending intervals permit creation during outages.
func (s *Store) CreateNotificationRule(ctx context.Context, actor monitoring.Actor, input notifications.RuleInput, activation []notifications.Activation) (notifications.Rule, error) {
	input, err := input.Canonical()
	if err != nil {
		return notifications.Rule{}, err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return notifications.Rule{}, err
	}
	defer tx.Rollback(ctx)
	if err = lockNotificationOwner(ctx, tx, actor, true); err != nil {
		return notifications.Rule{}, err
	}
	if err = notificationAdmission(ctx, tx, actor.Account.ID, true, false); err != nil {
		return notifications.Rule{}, err
	}
	id, err := newID()
	if err != nil {
		return notifications.Rule{}, err
	}
	var now time.Time
	if err = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return notifications.Rule{}, err
	}
	next := notifications.Rule{ID: id, AccountID: actor.Account.ID, Revision: 1, RuleInput: input, Activation: activation, CreatedAt: now.UTC(), UpdatedAt: now.UTC()}
	if err = persistNotificationRule(ctx, tx, nil, next, false, "notification_rule.created"); err != nil {
		return notifications.Rule{}, err
	}
	return next, tx.Commit(ctx)
}

func (s *Store) UpdateNotificationRule(ctx context.Context, actor monitoring.Actor, id string, expectedRevision int64, input notifications.RuleInput, activation []notifications.Activation) (notifications.Rule, error) {
	canonical, err := input.Canonical()
	if err != nil {
		return notifications.Rule{}, err
	}
	return s.changeNotificationRule(ctx, actor, id, expectedRevision, &canonical, activation, false)
}

// TransitionNotificationRule changes only per-namespace activation intervals.
// It has the same owner and revision fences as edits. A fresh active boundary
// is required after pending state or grant loss; unchanged siblings are retained.
func (s *Store) TransitionNotificationRule(ctx context.Context, actor monitoring.Actor, id string, expectedRevision int64, activation []notifications.Activation) (notifications.Rule, error) {
	return s.changeNotificationRule(ctx, actor, id, expectedRevision, nil, activation, false)
}

func (s *Store) DeleteNotificationRule(ctx context.Context, actor monitoring.Actor, id string, expectedRevision int64) (notifications.Rule, error) {
	return s.changeNotificationRule(ctx, actor, id, expectedRevision, nil, nil, true)
}

func (s *Store) changeNotificationRule(ctx context.Context, actor monitoring.Actor, id string, expectedRevision int64, input *notifications.RuleInput, activation []notifications.Activation, remove bool) (notifications.Rule, error) {
	if expectedRevision <= 0 || expectedRevision == math.MaxInt64 {
		return notifications.Rule{}, notifications.ErrConflict
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return notifications.Rule{}, err
	}
	defer tx.Rollback(ctx)
	if err = lockNotificationOwner(ctx, tx, actor, true); err != nil {
		return notifications.Rule{}, err
	}
	previous, err := readNotificationRule(ctx, tx, actor.Account.ID, id)
	if err != nil {
		return notifications.Rule{}, err
	}
	if previous.Revision != expectedRevision {
		return notifications.Rule{}, notifications.ErrConflict
	}
	next := previous
	if input != nil {
		next.RuleInput = *input
	}
	next.Activation = activation
	if remove {
		next.Enabled = false
		next.Activation = make([]notifications.Activation, len(next.Namespaces))
		for i, ref := range next.Namespaces {
			activationID, err := newID()
			if err != nil {
				return notifications.Rule{}, err
			}
			next.Activation[i] = notifications.Activation{ID: activationID, DeploymentID: ref.DeploymentID, NamespaceID: ref.NamespaceID, Status: notifications.ActivationDisabled}
		}
	}
	// Identical updates are harmless retries and do not consume history/rate or
	// the safety reserve. Expected revision and current identity still apply.
	priorJSON, _ := json.Marshal(previous)
	nextJSON, _ := json.Marshal(next)
	if !remove && bytes.Equal(priorJSON, nextJSON) {
		return previous, tx.Commit(ctx)
	}
	stop := remove || previous.Enabled && !next.Enabled
	if err = notificationAdmission(ctx, tx, actor.Account.ID, false, stop); err != nil {
		return notifications.Rule{}, err
	}
	var now time.Time
	if err = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return notifications.Rule{}, err
	}
	next.Revision++
	next.UpdatedAt = now.UTC()
	if next.UpdatedAt.Before(previous.UpdatedAt) {
		next.UpdatedAt = previous.UpdatedAt
	}
	if remove {
		deleted := next.UpdatedAt
		next.DeletedAt = &deleted
	}
	action := "notification_rule.updated"
	if remove {
		action = "notification_rule.deleted"
	} else if input == nil {
		action = "notification_rule.activation_changed"
	}
	if err = persistNotificationRule(ctx, tx, &previous, next, stop, action); err != nil {
		return notifications.Rule{}, err
	}
	return next, tx.Commit(ctx)
}

// DisableNotificationActivations is a monotonic current-authority denial fence.
// It consumes no rule revision, history allowance or mutation rate allowance.
// The whole selection must still refer to the current revision's exact interval
// IDs. Existing tombstones never clear, including after directory regrant.
func (s *Store) DisableNotificationActivations(ctx context.Context, actor monitoring.Actor, id string, expectedRevision int64, activationIDs []string) error {
	if expectedRevision <= 0 || len(activationIDs) < 1 || len(activationIDs) > notifications.MaximumNamespaces {
		return notifications.ErrInvalid
	}
	activationIDs = slices.Clone(activationIDs)
	slices.Sort(activationIDs)
	if len(slices.Compact(slices.Clone(activationIDs))) != len(activationIDs) {
		return notifications.ErrInvalid
	}
	for _, activationID := range activationIDs {
		if !eventUUID(activationID) {
			return notifications.ErrInvalid
		}
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = lockNotificationOwner(ctx, tx, actor, true); err != nil {
		return err
	}
	rule, err := readNotificationRule(ctx, tx, actor.Account.ID, id)
	if err != nil {
		return err
	}
	if rule.Revision != expectedRevision {
		return notifications.ErrConflict
	}
	current := make(map[string]bool, len(rule.Activation))
	for _, activation := range rule.Activation {
		current[activation.ID] = true
	}
	for _, activationID := range activationIDs {
		if !current[activationID] {
			return notifications.ErrConflict
		}
	}
	tag, err := tx.Exec(ctx, `INSERT INTO dashboard_notification_activation_revocations(activation_id) SELECT id FROM unnest($1::uuid[]) AS selected(id) ON CONFLICT DO NOTHING`, activationIDs)
	if err != nil {
		return err
	}
	if tag.RowsAffected() > 0 {
		if _, err = tx.Exec(ctx, `INSERT INTO dashboard_audit(account_id,action,resource_kind,resource_id) VALUES($1::uuid,'notification_rule.activations_revoked','notification_rule',$2)`, actor.Account.ID, id); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// NotificationActivationRevocations returns only the current rule's revoked
// interval IDs. Callers must compare against this exact revision before matching
// or sending. Immutable snapshots alone do not confer current delivery authority.
func (s *Store) NotificationActivationRevocations(ctx context.Context, actor monitoring.Actor, id string, expectedRevision int64) ([]string, error) {
	if expectedRevision <= 0 {
		return nil, notifications.ErrConflict
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if err = lockNotificationOwner(ctx, tx, actor, false); err != nil {
		return nil, err
	}
	rule, err := readNotificationRule(ctx, tx, actor.Account.ID, id)
	if err != nil {
		return nil, err
	}
	if rule.Revision != expectedRevision {
		return nil, notifications.ErrConflict
	}
	ids := make([]string, len(rule.Activation))
	for i, activation := range rule.Activation {
		ids[i] = activation.ID
	}
	rows, err := tx.Query(ctx, `SELECT a.id::text FROM dashboard_notification_activations a
 LEFT JOIN dashboard_notification_activation_revocations r ON r.activation_id=a.id
 LEFT JOIN dashboard_notification_scope_revocations n ON n.deployment_id=a.deployment_id AND n.namespace_id=a.namespace_id
 WHERE a.id=ANY($1::uuid[]) AND (r.activation_id IS NOT NULL OR a.origin_recorded_at<=n.revoked_through) ORDER BY a.id`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	revoked := []string{}
	for rows.Next() {
		var activationID string
		if err = rows.Scan(&activationID); err != nil {
			return nil, err
		}
		revoked = append(revoked, activationID)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return revoked, tx.Commit(ctx)
}

// NotificationFeed is a local read-only snapshot for the activation bridge.
// It never acquires or returns a worker lease, advances a cursor, initializes a
// source or hides a paused/initializing state. No source HTTP occurs here.
func (s *Store) NotificationFeed(ctx context.Context, deployment string) (events.Feed, error) {
	var feed events.Feed
	if !eventUUID(deployment) {
		return feed, notifications.ErrInvalid
	}
	var encoded []byte
	err := s.Pool.QueryRow(ctx, `SELECT deployment_id::text,namespace_ids::text[],status,generation,checkpoint,cursor,last_position FROM dashboard_event_feeds WHERE deployment_id=$1::uuid`, deployment).Scan(&feed.DeploymentID, &feed.NamespaceIDs, &feed.Status, &feed.Generation, &encoded, &feed.Cursor, &feed.LastPosition)
	if errors.Is(err, pgx.ErrNoRows) {
		return feed, notifications.ErrInactiveFeed
	}
	if err != nil {
		return feed, err
	}
	canonical, err := feedNamespaces(feed.NamespaceIDs)
	if err != nil || !slices.Equal(canonical, feed.NamespaceIDs) || feed.Generation <= 0 || feed.LastPosition < 0 || feed.Status != "active" && feed.Status != "initializing" && feed.Status != "paused" {
		return events.Feed{}, notifications.ErrInvalid
	}
	if encoded != nil {
		if json.Unmarshal(encoded, &feed.Checkpoint) != nil || feed.Checkpoint.Validate() != nil || feed.Checkpoint.DeploymentID != deployment || !slices.Equal(feed.NamespaceIDs, feed.Checkpoint.NamespaceIDs) {
			return events.Feed{}, notifications.ErrInvalid
		}
	}
	if feed.Status == "active" && (encoded == nil || !events.ValidCursor(feed.Cursor)) {
		return events.Feed{}, notifications.ErrInvalid
	}
	return feed, nil
}
