package store

import (
	"testing"
	"time"

	"github.com/ryancswallace/jobman-dashboard/internal/auth"
	"github.com/ryancswallace/jobman-dashboard/internal/config"
)

func TestConfiguredPreferencesInitializeNewAccountsOnly(t *testing.T) {
	s := testDB(t)
	ctx := t.Context()
	s.userDefaults = &config.UserDefaults{Timezone: "America/New_York", Appearance: "dark", RefreshSeconds: 5}
	identity := auth.Identity{Issuer: "https://synthetic.example", Subject: "web", DirectoryID: "44444444-4444-4444-8444-444444444444", DisplayName: "Example"}
	actor, err := s.ResolveIdentity(ctx, identity)
	if err != nil {
		t.Fatal(err)
	}
	check := func() {
		t.Helper()
		var zone, appearance string
		var seconds int64
		if err := s.Pool.QueryRow(ctx, `SELECT timezone,appearance,refresh_seconds FROM dashboard_preferences WHERE account_id=$1::uuid`, actor.Account.ID).Scan(&zone, &appearance, &seconds); err != nil {
			t.Fatal(err)
		}
		if zone != "America/New_York" || appearance != "dark" || seconds != 5 {
			t.Fatal("initial preferences changed")
		}
	}
	check()
	s.userDefaults = &config.UserDefaults{Timezone: "UTC", Appearance: "light", RefreshSeconds: 30}
	identity.Subject = "native"
	if _, err := s.ResolveIdentity(ctx, identity); err != nil {
		t.Fatal(err)
	}
	check()
}

func TestConfiguredReportRetentionPreservesExistingExpiry(t *testing.T) {
	s := testDB(t)
	actor := reportActor(t, s, 1)
	s.retention = &config.Retention{ReportDays: 90, InboxDays: 7}
	first, err := s.EnqueueReport(t.Context(), actor, reportSubject(), "retention-request-01")
	if err != nil {
		t.Fatal(err)
	}
	if delta := first.ExpiresAt.Sub(first.CreatedAt); delta < 90*24*time.Hour-time.Second || delta > 90*24*time.Hour+time.Second {
		t.Fatalf("incorrect future expiry %s", delta)
	}
	s.retention = &config.Retention{ReportDays: 1, InboxDays: 1}
	retained, err := s.EnqueueReport(t.Context(), actor, reportSubject(), "retention-request-02")
	if err != nil {
		t.Fatal(err)
	}
	if retained.ID != first.ID || !retained.ExpiresAt.Equal(first.ExpiresAt) {
		t.Fatal("retention changed existing record")
	}
}

func TestConfiguredInboxRetention(t *testing.T) {
	x := evaluationSetup(t)
	x.s.retention = &config.Retention{ReportDays: 30, InboxDays: 7}
	actor := reportActor(t, x.s, 1)
	x.rule(t, actor)
	x.event(t, 1)
	x.fanout(t)
	claim := x.claim(t)
	if _, err := x.repo.CommitNotificationEvaluation(t.Context(), claim, x.fence(t), x.authorized(t, claim)); err != nil {
		t.Fatal(err)
	}
	if x.count(t, `SELECT count(*) FROM dashboard_notification_inbox WHERE expires_at-created_at BETWEEN interval '6 days 23 hours' AND interval '7 days 1 hour'`) != 1 {
		t.Fatal("configured inbox expiry not used")
	}
}
