package reloader

import (
	"context"
	"github.com/Soli0222/mf-account-reloader/internal/failure"
	"github.com/Soli0222/mf-account-reloader/internal/moneyforward"
	"testing"
	"time"
)

type mockPage struct {
	calls     []string
	failBulk  bool
	states    []moneyforward.Snapshot
	at        int
	snapshots int
}

func (m *mockPage) Snapshot(context.Context) (moneyforward.Snapshot, error) {
	m.snapshots++
	if m.snapshots > 1 && len(m.states) > 1 {
		return m.states[1], nil
	}
	return m.states[0], nil
}
func (m *mockPage) Refresh(context.Context) (moneyforward.Snapshot, error) {
	if m.at+1 < len(m.states) {
		m.at++
	}
	return m.states[m.at], nil
}
func (m *mockPage) Update(ctx context.Context, id *string) (string, error) {
	if id == nil {
		m.calls = append(m.calls, "bulk")
		if m.failBulk {
			return "unknown", failure.New(4, "unknown receipt")
		}
		return "accepted", nil
	}
	m.calls = append(m.calls, *id)
	return "accepted", nil
}
func TestBulkFailureStopsExtras(t *testing.T) {
	m := &mockPage{failBulk: true, states: []moneyforward.Snapshot{{BulkCount: 1, Accounts: []moneyforward.Account{{ID: "a", Automatic: true, CanUpdate: true}}}}}
	r, e := Run(context.Background(), m, Options{IDs: []string{"a"}})
	if failure.Code(e) != 4 || len(m.calls) != 1 || r.Bulk != "unknown" {
		t.Fatal(r, e, m.calls)
	}
}
func TestMinimumIntervalAndCompleteTargets(t *testing.T) {
	s := moneyforward.Snapshot{BulkCount: 1, Accounts: []moneyforward.Account{{ID: "a", Automatic: true, CanUpdate: true, Status: "ok", ObservedUpdating: true}, {ID: "b", Status: "manual"}, {ID: "c", Automatic: true, Status: "auth_required"}}}
	m := &mockPage{states: []moneyforward.Snapshot{s}}
	var delays []time.Duration
	r, e := Run(context.Background(), m, Options{IDs: []string{"a"}, Wait: true, PollInterval: time.Second, Pause: func(_ context.Context, d time.Duration) error { delays = append(delays, d); return nil }})
	if e != nil || r.Bulk != "completed" || r.Excluded != 2 || len(delays) != 1 || delays[0] < time.Second {
		t.Fatal(r, e, delays)
	}
}

func TestWaitRequiresEvidenceForEveryTarget(t *testing.T) {
	for _, observed := range []bool{false, true} {
		s := moneyforward.Snapshot{BulkCount: 1, Accounts: []moneyforward.Account{
			{ID: "a", Automatic: true, Status: "ok", ObservedUpdating: observed},
			{ID: "b", Automatic: true, Status: "ok"},
		}}
		m := &mockPage{states: []moneyforward.Snapshot{s}}
		r, e := Run(context.Background(), m, Options{Wait: true, PollInterval: time.Second, Pause: func(context.Context, time.Duration) error { return context.DeadlineExceeded }})
		if failure.Code(e) != 5 || r.Bulk != "accepted" {
			t.Fatalf("stale normal status was reported as complete: %+v %v", r, e)
		}
	}
}
func TestWaitIncludesPostUpdateSnapshot(t *testing.T) {
	before := moneyforward.Snapshot{BulkCount: 1, Accounts: []moneyforward.Account{{ID: "a", Automatic: true, Status: "ok"}}}
	after := before
	after.Accounts = append(append([]moneyforward.Account{}, before.Accounts...), moneyforward.Account{ID: "new-linked", Automatic: true, Status: "updating"})
	m := &mockPage{states: []moneyforward.Snapshot{before, after}}
	r, e := Run(context.Background(), m, Options{Wait: true, PollInterval: time.Second, Pause: func(context.Context, time.Duration) error { return context.DeadlineExceeded }})
	if failure.Code(e) != 5 || r.Bulk != "accepted" {
		t.Fatalf("newly visible update was not waited on: %+v %v", r, e)
	}
}
