package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ryancswallace/jobman-dashboard/internal/api"
	"github.com/ryancswallace/jobman-dashboard/internal/auth"
	"github.com/ryancswallace/jobman-dashboard/internal/notifications"
	"github.com/ryancswallace/jobman-dashboard/internal/push"
)

func runtimeRoleStores(t *testing.T, s *Store) map[string]*Store {
	t.Helper()
	ctx := t.Context()
	var elevated bool
	if err := s.Pool.QueryRow(ctx, `SELECT rolsuper FROM pg_roles WHERE rolname=current_user`).Scan(&elevated); err != nil {
		t.Fatal(err)
	}
	if !elevated {
		t.Skip("role integration requires an explicitly supplied disposable-test administrator")
	}
	var schema, database string
	if err := s.Pool.QueryRow(ctx, `SELECT current_schema(),current_database()`).Scan(&schema, &database); err != nil {
		t.Fatal(err)
	}
	out := map[string]*Store{}
	for _, component := range []string{"api", "ingestion", "notifications", "delivery", "reports", "retention", "operator"} {
		raw := make([]byte, 16)
		if _, err := rand.Read(raw); err != nil {
			t.Fatal(err)
		}
		name := "dashboard_role_test_" + hex.EncodeToString(raw[:6]) + "_" + component
		password := hex.EncodeToString(raw)
		_, err := s.Pool.Exec(ctx, "CREATE ROLE "+pgx.Identifier{name}.Sanitize()+" LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOREPLICATION NOBYPASSRLS PASSWORD '"+password+"'")
		if err != nil {
			t.Fatal("could not create isolated test role")
		}
		var pool *pgxpool.Pool
		t.Cleanup(func() {
			if pool != nil {
				pool.Close()
			}
			cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_, err := s.Pool.Exec(cleanup, "DROP OWNED BY "+pgx.Identifier{name}.Sanitize()+"; DROP ROLE "+pgx.Identifier{name}.Sanitize())
			if err != nil {
				t.Error("temporary role cleanup failed")
			}
		})
		if _, err = s.Pool.Exec(ctx, "GRANT CONNECT ON DATABASE "+pgx.Identifier{database}.Sanitize()+" TO "+pgx.Identifier{name}.Sanitize()); err != nil {
			t.Fatal(err)
		}
		command := exec.CommandContext(ctx, "python3", filepath.Join("..", "..", "deploy", "postgres", "grants.py"), "--schema", schema, "--role", name, "--component", component)
		plan, err := command.Output()
		if err != nil {
			t.Fatal("grant renderer failed")
		}
		if _, err = s.Pool.Exec(ctx, string(plan)); err != nil {
			t.Fatal("grant application", component, err)
		}
		cfg, err := pgxpool.ParseConfig(os.Getenv("JOBMAN_DASHBOARD_TEST_DATABASE_URL"))
		if err != nil {
			t.Fatal("test connection configuration")
		}
		cfg.ConnConfig.User = name
		cfg.ConnConfig.Password = password
		cfg.ConnConfig.RuntimeParams["search_path"] = schema
		cfg.MaxConns = 1
		pool, err = pgxpool.NewWithConfig(ctx, cfg)
		if err != nil {
			t.Fatal("role connection failed")
		}
		roleStore := &Store{Pool: pool}
		out[component] = roleStore
		if err = roleStore.CheckSchema(ctx); err != nil {
			t.Fatal(component, err)
		}
		var current string
		if err = pool.QueryRow(ctx, `SELECT current_user`).Scan(&current); err != nil || current != name {
			t.Fatal("test is not running as its isolated role")
		}
	}
	return out
}

func roleDenied(t *testing.T, s *Store, query string) {
	t.Helper()
	tx, err := s.Pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(t.Context())
	_, err = tx.Exec(t.Context(), query)
	var pg *pgconn.PgError
	if !errors.As(err, &pg) || pg.Code != "42501" {
		t.Fatalf("expected privilege denial for %s: %v", query, err)
	}
}

func TestRuntimeRolesEndToEndAndPrivilegeDenials(t *testing.T) {
	s := testDB(t)
	role := runtimeRoleStores(t, s)
	ctx := t.Context()
	// API creates verified identity and state; workers use the same immutable IDs.
	actor := reportActor(t, role["api"], 990)
	if _, err := role["api"].ResolveIdentity(ctx, auth.Identity{Issuer: actor.Issuer, Subject: actor.Subject, DirectoryID: actor.DirectoryID, DisplayName: "Updated synthetic"}); err != nil {
		t.Fatal(err)
	}
	if _, err := role["api"].UpdatePreferences(ctx, actor.Account.ID, "1", api.DefaultPreferences()); err != nil {
		t.Fatal(err)
	}
	token := make([]byte, 32)
	csrf := make([]byte, 32)
	token[0] = 1
	csrf[0] = 2
	if err := role["api"].PutLogin(ctx, token, []byte("synthetic"), time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := role["api"].ConsumeLogin(ctx, token); err != nil {
		t.Fatal(err)
	}
	if err := role["api"].CreateSession(ctx, auth.Session{Actor: actor, TokenHash: token, CSRFHash: csrf, CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if _, err := role["api"].Session(ctx, token); err != nil {
		t.Fatal(err)
	}
	cursor, err := role["api"].Create(ctx, []byte(`{"accountId":"synthetic","queryHash":"roles"}`), time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = role["api"].Load(ctx, cursor); err != nil {
		t.Fatal(err)
	}
	if err = role["api"].Advance(ctx, cursor, 1, nil); err != nil {
		t.Fatal(err)
	}
	cp := eventCheckpoint()
	var now time.Time
	if err = s.Pool.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		t.Fatal(err)
	}
	cp.AsOf = now.Add(-time.Minute)
	for _, r := range []string{"api", "ingestion", "notifications", "delivery", "reports"} {
		if err = role[r].VerifySourceIdentity(ctx, cp.DeploymentID, cp.ControlInstanceID, cp.RecoveryEpoch, 1); err != nil {
			t.Fatal(r, err)
		}
	}
	feed := initializedFeed(t, role["ingestion"], cp)
	in := notificationInput(cp)
	rule, err := role["api"].CreateNotificationRule(ctx, actor, in, activeNotificationIntervals(t, cp, feed))
	if err != nil {
		t.Fatal("API rule", err)
	}
	d := deviceStore(t, role["api"])
	registration, _ := deviceBind(t, d, actor, 911)
	policy, _ := notifications.NewDevicePolicy([]notifications.DeviceTopic{{Topic: registration.Topic, Environment: registration.Environment}})
	evaluator, err := NewNotificationEvaluationStore(role["notifications"], actor.Issuer, policy)
	if err != nil {
		t.Fatal(err)
	}
	fixture := &evaluationFixture{s: role["ingestion"], repo: evaluator, cp: cp, feed: feed, now: now}
	event := fixture.event(t, 1)
	fixture.s = role["notifications"]
	fixture.fanout(t)
	claim := fixture.claim(t)
	decision := fixture.authorized(t, claim)
	result, err := evaluator.CommitNotificationEvaluation(ctx, claim, fixture.fence(t), decision)
	if err != nil || result.Deliveries != 1 {
		t.Fatal("evaluation", result, err)
	}
	if _, err = role["api"].NotificationInbox(ctx, actor, result.InboxID); err != nil {
		t.Fatal(err)
	}
	if _, err = role["api"].SetNotificationInboxRead(ctx, actor, result.InboxID, true, decision.Proof); err != nil {
		t.Fatal("inbox read", err)
	}
	deliveryEvaluator, _ := NewNotificationEvaluationStore(role["delivery"], actor.Issuer, policy)
	deliveryDevices := deviceStore(t, role["delivery"])
	sender, err := NewNotificationDeliveryStore(deliveryEvaluator, deliveryDevices, []notifications.DeviceTopic{{Topic: registration.Topic, Environment: registration.Environment}})
	if err != nil {
		t.Fatal(err)
	}
	dc, err := sender.ClaimNotificationDelivery(ctx, fixture.fence(t))
	if err != nil {
		t.Fatal("delivery claim", err)
	}
	handoff, err := sender.PrepareNotificationDelivery(ctx, dc, fixture.fence(t), decision)
	if err != nil {
		t.Fatal("delivery prepare", err)
	}
	if err = sender.FinishNotificationDelivery(ctx, handoff, push.Result{Outcome: "token_invalid", Reason: "Unregistered"}); err != nil {
		t.Fatal("delivery invalidation", err)
	}
	// Worker may advance only an existing pending rule's activation intervals.
	pending, err := role["api"].CreateNotificationRule(ctx, actor, in, notificationIntervals(t, in, notifications.ActivationPending))
	if err != nil {
		t.Fatal(err)
	}
	activationStore, _ := NewNotificationActivationStore(role["notifications"], actor.Issuer)
	ac, err := activationStore.ClaimNotificationActivation(ctx)
	if err != nil || ac.RuleID != pending.ID {
		t.Fatal("activation claim", err)
	}
	if _, err = role["notifications"].TransitionNotificationRule(ctx, actor, pending.ID, pending.Revision, activeNotificationIntervals(t, cp, feed)); err != nil {
		t.Fatal("activation transition", err)
	}
	if err = role["delivery"].DisableNotificationActivations(ctx, actor, rule.ID, rule.Revision, []string{rule.Activation[0].ID}); err != nil {
		t.Fatal("current denial", err)
	}
	task, err := role["api"].EnqueueReport(ctx, actor, reportSubject(), "role-test-request")
	if err != nil {
		t.Fatal(err)
	}
	reportClaim, err := role["reports"].ClaimReport(ctx)
	if err != nil {
		t.Fatal("report claim", err)
	}
	if err = role["reports"].AnalyzeReport(ctx, task.ID, reportClaim.Task.LeaseToken); err != nil {
		t.Fatal(err)
	}
	if err = role["reports"].CompleteReport(ctx, task.ID, reportClaim.Task.LeaseToken, reportObject(task.ID), actor); err != nil {
		t.Fatal("report complete", err)
	}
	if _, err = role["api"].ReportTask(ctx, actor.Account.ID, task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = role["operator"].OperationalStatus(ctx, []string{cp.DeploymentID}); err != nil {
		t.Fatal("operator status", err)
	}

	pressure, err := role["operator"].OperationalReportPressure(ctx, []string{reportSubject().DeploymentID})
	if err != nil || pressure.Pending != "0" || pressure.Leased != "0" {
		t.Fatal("operator report pressure", err)
	}
	roleDenied(t, role["operator"], `SELECT object FROM dashboard_report_tasks`)
	roleDenied(t, role["operator"], `SELECT subject FROM dashboard_report_tasks`)
	// Real retention statements run under their role, including invoker triggers.
	if err = role["retention"].PruneAuthentication(ctx); err != nil {
		t.Fatal("auth retention", err)
	}
	if _, err = role["retention"].PruneCursors(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = role["retention"].ExpireNotificationDeliveries(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = role["retention"].PruneNotifications(ctx); err != nil {
		t.Fatal("notification retention", err)
	}
	if _, err = role["retention"].DeleteExpiredReports(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = role["retention"].PruneSourceEvents(ctx, event.DeploymentID); err != nil {
		t.Fatal(err)
	}
	for name, r := range role {
		t.Run(name+" denies protected mutation", func(t *testing.T) {
			for _, q := range []string{`UPDATE dashboard_accounts SET directory_id=id`, `UPDATE dashboard_accounts SET disabled_at=NULL`, `UPDATE dashboard_identity_aliases SET subject='intruder'`, `UPDATE dashboard_schema_migrations SET sha256='changed'`, `CREATE TABLE forbidden_table(id int)`, `UPDATE dashboard_notification_delivery_control SET held=false,generation=generation+1`} {
				roleDenied(t, r, q)
			}
			if name != "api" {
				for _, q := range []string{`UPDATE dashboard_accounts SET display_name='intruder'`, `INSERT INTO dashboard_identity_aliases(issuer,subject,account_id) SELECT 'intruder','intruder',id FROM dashboard_accounts`, `UPDATE dashboard_preferences SET appearance='dark'`, `UPDATE dashboard_sessions SET touched_at=clock_timestamp()`, `SELECT encrypted_refresh_token FROM dashboard_sessions`, `SELECT secret_hash FROM dashboard_notification_installations`, `UPDATE dashboard_notification_device_bindings SET enabled=true`, `UPDATE dashboard_notification_device_revocations SET state='active'`} {
					roleDenied(t, r, q)
				}
			}
		})
	}
	roleDenied(t, role["api"], `UPDATE dashboard_report_tasks SET state='queued'`)
	roleDenied(t, role["ingestion"], `SELECT payload FROM dashboard_notification_rules`)
	roleDenied(t, role["reports"], `SELECT token_ciphertext FROM dashboard_notification_device_bindings`)
	roleDenied(t, role["operator"], `SELECT payload FROM dashboard_source_events`)
	roleDenied(t, role["operator"], `SELECT id FROM dashboard_accounts`)
	roleDenied(t, role["operator"], `SELECT deployment_id FROM dashboard_event_feeds FOR SHARE`)
	// Every lock guard either rejects a no-op through its existing immutable
	// trigger or leaves all serialized row fields unchanged, including freshness.
	guards := map[string]string{
		"accounts": "notifications", "identity_aliases": "notifications",
		"sessions": "retention", "login_attempts": "retention", "audit": "retention",
		"browse_sessions": "retention", "event_feeds": "notifications",
		"notification_delivery_control": "notifications", "notification_inbox": "delivery",
		"report_tasks": "retention", "notification_rules": "retention", "notification_rule_versions": "retention",
		"notification_delivery_attempts": "retention", "notification_installations": "notifications", "notification_device_bindings": "notifications",
	}
	for suffix, component := range guards {
		table := "dashboard_" + suffix
		var before, after string
		query := "SELECT md5(COALESCE(string_agg(to_jsonb(t)::text,',' ORDER BY to_jsonb(t)::text),'')) FROM " + table + " t"
		if err = s.Pool.QueryRow(ctx, query).Scan(&before); err != nil {
			t.Fatal(err)
		}
		_, err = role[component].Pool.Exec(ctx, "UPDATE "+table+" SET runtime_lock=DEFAULT")
		if err != nil {
			var pg *pgconn.PgError
			if !errors.As(err, &pg) || (pg.Code != "23514" && pg.Code != "P0001") {
				t.Fatal("guard no-op unexpected rejection", table, err)
			}
		}
		if err = s.Pool.QueryRow(ctx, query).Scan(&after); err != nil || before != after {
			t.Fatal("lock-only default mutated fields", table, err)
		}
		_, err = role[component].Pool.Exec(ctx, "UPDATE "+table+" SET runtime_lock=false")
		var pg *pgconn.PgError
		if !errors.As(err, &pg) || pg.Code != "428C9" {
			t.Fatal("generated guard accepted write", table, err)
		}
	}

	// Ledger stays matched and legacy owner-created source state survives.
	if err = s.CheckSchema(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeRolesRetentionInvokersAndUpgrade(t *testing.T) {
	x := deliverySetup(t)
	// Recreate the pre18 shape only in this disposable fixture, then exercise the
	// real forward migrator against populated auth/device/event/history rows.
	rows, err := x.s.Pool.Query(t.Context(), `SELECT table_name FROM information_schema.columns WHERE table_schema=current_schema() AND column_name='runtime_lock' ORDER BY table_name`)
	if err != nil {
		t.Fatal(err)
	}
	tables := []string{}
	for rows.Next() {
		var table string
		if err = rows.Scan(&table); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, table)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	if len(tables) != 15 {
		t.Fatal("unexpected migration lock-only set", len(tables))
	}
	before := map[string]string{}
	for _, table := range tables {
		var value string
		if err = x.s.Pool.QueryRow(t.Context(), "SELECT md5(COALESCE(string_agg((to_jsonb(t)-'runtime_lock')::text,',' ORDER BY (to_jsonb(t)-'runtime_lock')::text),'')) FROM "+pgx.Identifier{table}.Sanitize()+" t").Scan(&value); err != nil {
			t.Fatal(err)
		}
		before[table] = value
	}
	for _, table := range tables {
		if _, err = x.s.Pool.Exec(t.Context(), "ALTER TABLE "+pgx.Identifier{table}.Sanitize()+" DROP COLUMN runtime_lock"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = x.s.Pool.Exec(t.Context(), `DELETE FROM dashboard_schema_migrations WHERE name='migrations/000018_runtime_lock_privileges.sql'`); err != nil {
		t.Fatal(err)
	}
	if err = x.s.Migrate(t.Context()); err != nil {
		t.Fatal("populated forward upgrade", err)
	}
	for _, table := range tables {
		var after string
		if err = x.s.Pool.QueryRow(t.Context(), "SELECT md5(COALESCE(string_agg((to_jsonb(t)-'runtime_lock')::text,',' ORDER BY (to_jsonb(t)-'runtime_lock')::text),'')) FROM "+pgx.Identifier{table}.Sanitize()+" t").Scan(&after); err != nil || after != before[table] {
			t.Fatal("upgrade changed existing fields", table, err)
		}
	}
	role := runtimeRoleStores(t, x.s)
	ctx := t.Context()
	// Upgrade was additive; an owner-created active device and delivery still work.
	if devices, err := deviceStore(t, role["api"]).List(ctx, x.actor); err != nil || len(devices) != 1 {
		t.Fatal("legacy device compatibility", err)
	}
	c := x.claimDelivery(t)
	h := x.prepare(t, c)
	if err := x.delivery.FinishNotificationDelivery(ctx, h, push.Result{Outcome: "accepted", ProviderID: c.ID}); err != nil {
		t.Fatal(err)
	}
	// Keep one old, unreferenced rule interval and tombstone for trigger retirement.
	old, ids := retentionRule(t, x.evaluationFixture, x.actor, 3, true)
	deviceExec(t, x.s, `INSERT INTO dashboard_notification_activation_revocations(activation_id) VALUES($1::uuid)`, ids[0])
	deviceExec(t, x.s, `UPDATE dashboard_notification_inbox SET created_at=clock_timestamp()-interval '31 days',expires_at=clock_timestamp()-interval '1 day'; UPDATE dashboard_notification_deliveries SET resolved_at=clock_timestamp()-interval '36 days'`)
	stats, err := role["retention"].PruneNotifications(ctx)
	if err != nil || stats.Inboxes != 1 || stats.Deliveries != 1 || stats.Attempts != 1 || stats.RuleVersions != 2 || stats.ActivationPayloads != 2 || stats.Revocations != 1 {
		t.Fatal("actual retirement with invoker triggers", stats, err)
	}
	if _, err = role["api"].NotificationRule(ctx, x.actor, old.ID); err != nil {
		t.Fatal("current rule survived", err)
	}
	deviceExec(t, x.s, `UPDATE dashboard_source_events SET expires_at=clock_timestamp()-interval '1 day',last_seen_at=clock_timestamp()-interval '36 days'`)
	if count, err := role["retention"].PruneSourceEvents(ctx, x.cp.DeploymentID); err != nil || count != 1 {
		t.Fatal("source retention", count, err)
	}
	task, err := role["api"].EnqueueReport(ctx, x.actor, reportSubject(), "expired-role-test")
	if err != nil {
		t.Fatal(err)
	}
	deviceExec(t, x.s, `UPDATE dashboard_report_tasks SET created_at=clock_timestamp()-interval '31 days',expires_at=clock_timestamp()-interval '1 day' WHERE id=$1::uuid`, task.ID)
	if _, err = role["retention"].DeleteExpiredReports(ctx); err != nil {
		t.Fatal("report cascades", err)
	}
	// Installer removes stale column-level grants, not only broad table grants.
	var schema, name string
	if err = x.s.Pool.QueryRow(ctx, `SELECT current_schema()`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	if err = role["delivery"].Pool.QueryRow(ctx, `SELECT current_user`).Scan(&name); err != nil {
		t.Fatal(err)
	}
	if _, err = x.s.Pool.Exec(ctx, "GRANT UPDATE(enabled) ON dashboard_notification_device_bindings TO "+pgx.Identifier{name}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	plan, err := exec.CommandContext(ctx, "python3", filepath.Join("..", "..", "deploy", "postgres", "grants.py"), "--schema", schema, "--role", name, "--component", "delivery").Output()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = x.s.Pool.Exec(ctx, string(plan)); err != nil {
		t.Fatal("grant replay", err)
	}
	roleDenied(t, role["delivery"], `UPDATE dashboard_notification_device_bindings SET enabled=true`)
}

func TestRuntimeRolesGrantPreflightIsTransactional(t *testing.T) {
	s := testDB(t)
	roles := runtimeRoleStores(t, s)
	ctx := t.Context()
	var schema, target, parent string
	if err := s.Pool.QueryRow(ctx, `SELECT current_schema()`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	if err := roles["delivery"].Pool.QueryRow(ctx, `SELECT current_user`).Scan(&target); err != nil {
		t.Fatal(err)
	}
	if err := roles["reports"].Pool.QueryRow(ctx, `SELECT current_user`).Scan(&parent); err != nil {
		t.Fatal(err)
	}
	plan, err := exec.CommandContext(ctx, "python3", filepath.Join("..", "..", "deploy", "postgres", "grants.py"), "--schema", schema, "--role", target, "--component", "delivery").Output()
	if err != nil {
		t.Fatal(err)
	}
	reject := func() {
		t.Helper()
		conn, err := s.Pool.Acquire(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Release()
		if _, err = conn.Exec(ctx, string(plan)); err == nil {
			t.Fatal("unsafe grant target accepted")
		}
		if _, err = conn.Exec(ctx, "ROLLBACK"); err != nil {
			t.Fatal(err)
		}
		if err = roles["delivery"].CheckSchema(ctx); err != nil {
			t.Fatal("failed plan damaged prior grants", err)
		}
	}
	deviceExec(t, s, "ALTER ROLE "+pgx.Identifier{target}.Sanitize()+" CREATEROLE")
	reject()
	deviceExec(t, s, "ALTER ROLE "+pgx.Identifier{target}.Sanitize()+" NOCREATEROLE")
	deviceExec(t, s, "GRANT "+pgx.Identifier{parent}.Sanitize()+" TO "+pgx.Identifier{target}.Sanitize())
	reject()
	deviceExec(t, s, "REVOKE "+pgx.Identifier{parent}.Sanitize()+" FROM "+pgx.Identifier{target}.Sanitize())
	deviceExec(t, s, "CREATE TABLE runtime_role_owner_probe(id int); ALTER TABLE runtime_role_owner_probe OWNER TO "+pgx.Identifier{target}.Sanitize())
	reject()
	deviceExec(t, s, "DROP TABLE runtime_role_owner_probe")
	// An inherited PUBLIC read on this disposable schema invalidates the plan;
	// no live schema/public privileges are involved.
	deviceExec(t, s, "GRANT SELECT(directory_id) ON dashboard_accounts TO PUBLIC")
	reject()
	deviceExec(t, s, "REVOKE SELECT(directory_id) ON dashboard_accounts FROM PUBLIC")
	roleDenied(t, roles["delivery"], `UPDATE dashboard_accounts SET display_name='intruder'`)
	// Column grants do not silently expose data added by a later migration,
	// either before or after this reviewed schema18 plan is reapplied.
	deviceExec(t, s, `ALTER TABLE dashboard_accounts ADD COLUMN future_sensitive text`)
	for _, r := range roles {
		roleDenied(t, r, `SELECT future_sensitive FROM dashboard_accounts`)
	}
	if _, err = s.Pool.Exec(ctx, string(plan)); err != nil {
		t.Fatal("explicit column replay", err)
	}
	roleDenied(t, roles["delivery"], `SELECT future_sensitive FROM dashboard_accounts`)
}

func TestRuntimeGrantRenderer(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "python3", filepath.Join("..", "..", "deploy", "postgres", "test_grants.py"))
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("offline grant renderer checks: %v\n%s", err, output)
	}
}
