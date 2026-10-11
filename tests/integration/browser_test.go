//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Soli0222/mf-account-reloader/internal/auth"
	"github.com/Soli0222/mf-account-reloader/internal/browser"
	"github.com/Soli0222/mf-account-reloader/internal/config"
	"github.com/Soli0222/mf-account-reloader/internal/failure"
	"github.com/Soli0222/mf-account-reloader/internal/moneyforward"
	"github.com/Soli0222/mf-account-reloader/internal/reloader"
	"github.com/Soli0222/mf-account-reloader/testdata"
)

func setup(t *testing.T, options map[string]any) (*moneyforward.Adapter, func() []string) {
	t.Helper()
	var mu sync.Mutex
	events := []string{}
	if options == nil {
		options = map[string]any{}
	}
	data, _ := json.Marshal(options)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/accounts":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(strings.Replace(testdata.Accounts, "OPTIONS", string(data), 1)))
		case "/update":
			var v struct {
				ID *string `json:"id"`
			}
			_ = json.NewDecoder(r.Body).Decode(&v)
			name := "bulk"
			if v.ID != nil {
				name = *v.ID
			}
			mu.Lock()
			events = append(events, name)
			mu.Unlock()
			w.WriteHeader(200)
		case "/login":
			_, _ = w.Write([]byte(`<input type="password">`))
		default:
			if r.Method != "POST" {
				w.WriteHeader(404)
				return
			}
			mu.Lock()
			events = append(events, "wrong")
			mu.Unlock()
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(server.Close)
	c := config.Defaults()
	c.Browser.NoSandbox = os.Getenv("MFR_TEST_NO_SANDBOX") == "true"
	c.Browser.ExecutablePath = os.Getenv("MFR_BROWSER_PATH")
	b, e := browser.Open(context.Background(), c)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(b.Close)
	a := moneyforward.New(b, 2*time.Second, 350*time.Millisecond)
	a.AccountsURL = server.URL + "/accounts"
	if e = b.Navigate(context.Background(), a.AccountsURL, 2*time.Second); e != nil {
		t.Fatal(e)
	}
	eventsFn := func() []string { mu.Lock(); defer mu.Unlock(); return append([]string{}, events...) }
	return a, eventsFn
}
func TestUpdates(t *testing.T) {
	cases := []struct {
		name       string
		site       map[string]any
		ids, names []string
		dry, wait  bool
		want       []string
		code       int
		bulk       string
	}{
		{name: "hidden updating must not skip extra", site: map[string]any{"statusTemplates": true}, ids: []string{"alpha"}, want: []string{"bulk", "alpha"}, bulk: "accepted"},
		{name: "hidden nested status must not make row ambiguous", site: map[string]any{"statusTemplates": true, "hiddenStatusCell": true}, ids: []string{"alpha"}, want: []string{"bulk", "alpha"}, bulk: "accepted"},
		{name: "visible updating still skips extra", site: map[string]any{"statusTemplates": true, "updating": true}, ids: []string{"alpha"}, want: []string{"bulk"}, bulk: "accepted"},
		{name: "hidden receipt must not prove acceptance", site: map[string]any{"hiddenReceiptAfterClick": true, "noReceipt": true}, want: []string{"bulk"}, code: 4, bulk: "unknown"},
		{name: "wait ignores inactive status templates", site: map[string]any{"statusTemplates": true, "bulkUpdating": true, "complete": true}, wait: true, want: []string{"bulk"}, bulk: "completed"},
		{name: "duplicate bulk control", site: map[string]any{"duplicateBulk": true}, code: 4},
		{name: "form submit control", site: map[string]any{"forms": true}, ids: []string{"alpha"}, want: []string{"bulk", "alpha"}, bulk: "accepted"},
		{name: "bulk starts selected update", site: map[string]any{"bulkUpdating": true}, ids: []string{"alpha"}, want: []string{"bulk"}, bulk: "accepted"},
		{name: "bulk only", want: []string{"bulk"}, bulk: "accepted"},
		{name: "deduplicate and redraw", ids: []string{"alpha", "beta"}, names: []string{"人工口座 A"}, want: []string{"bulk", "alpha", "beta"}, bulk: "accepted"},
		{name: "exact comma name", names: []string{"人工口座 B, 外貨"}, want: []string{"bulk", "beta"}, bulk: "accepted"},
		{name: "ambiguous name preflight", site: map[string]any{"duplicateName": true}, names: []string{"人工口座 A"}, code: 2},
		{name: "missing ID preflight", ids: []string{"not-existing"}, code: 2},
		{name: "manual preflight", ids: []string{"manual"}, code: 2},
		{name: "disabled preflight", site: map[string]any{"disabled": true}, ids: []string{"alpha"}, code: 2},
		{name: "no stable ID", site: map[string]any{"missingId": true}, names: []string{"人工口座 A"}, code: 2},
		{name: "duplicate control", site: map[string]any{"duplicateControl": true}, ids: []string{"alpha"}, code: 2},
		{name: "dry run", ids: []string{"alpha"}, dry: true, bulk: "planned"},
		{name: "already updating", site: map[string]any{"updating": true}, ids: []string{"alpha"}, want: []string{"bulk"}, bulk: "accepted"},
		{name: "unknown receipt no resend", site: map[string]any{"noReceipt": true}, ids: []string{"alpha"}, want: []string{"bulk"}, code: 4, bulk: "unknown"},
		{name: "partial failure continues", site: map[string]any{"fail": "alpha"}, ids: []string{"alpha", "beta"}, want: []string{"bulk", "alpha", "beta"}, code: 4, bulk: "accepted"},
		{name: "unexpected dialog", site: map[string]any{"dialog": "削除しますか？"}, code: 4, bulk: "failed"},
		{name: "known bulk confirmation", site: map[string]any{"dialog": "金融機関からのデータ一括更新を行いますか？"}, want: []string{"bulk"}, bulk: "accepted"},
		{name: "lost auth", site: map[string]any{"redirect": true}, want: []string{"bulk"}, code: 3, bulk: "failed"},
		{name: "link controls", site: map[string]any{"links": true}, ids: []string{"alpha"}, want: []string{"bulk", "alpha"}, bulk: "accepted"},
		{name: "wait completed", site: map[string]any{"bulkUpdating": true, "complete": true}, wait: true, want: []string{"bulk"}, bulk: "completed"},
		{name: "wait failed", site: map[string]any{"bulkUpdating": true, "complete": true, "waitFailure": true}, wait: true, want: []string{"bulk"}, code: 4, bulk: "accepted"},
		{name: "wait disappeared", site: map[string]any{"bulkUpdating": true, "complete": true, "disappear": true}, wait: true, want: []string{"bulk"}, code: 4, bulk: "accepted"},
		{name: "wait deadline", site: map[string]any{"bulkUpdating": true}, wait: true, want: []string{"bulk"}, code: 5, bulk: "accepted"},
		{name: "wait unknown preflight", site: map[string]any{"unknown": true}, wait: true, code: 4},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			a, events := setup(t, tt.site)
			ctx, cancel := context.WithTimeout(context.Background(), 1300*time.Millisecond)
			defer cancel()
			result, e := reloader.Run(ctx, a, reloader.Options{IDs: tt.ids, Names: tt.names, DryRun: tt.dry, Wait: tt.wait, PollInterval: 50 * time.Millisecond, MinInterval: time.Millisecond})
			if failure.Code(e) != tt.code {
				t.Fatalf("code=%d want %d: %v result=%+v", failure.Code(e), tt.code, e, result)
			}
			got := events()
			if len(got) != len(tt.want) || strings.Join(got, "|") != strings.Join(tt.want, "|") {
				t.Fatalf("requests=%v want=%v", got, tt.want)
			}
			if tt.bulk != "" && result.Bulk != tt.bulk {
				t.Fatalf("bulk=%s want=%s", result.Bulk, tt.bulk)
			}
		})
	}
}
func TestReadOnlyCommands(t *testing.T) {
	a, events := setup(t, nil)
	ctx := context.Background()
	manager := auth.Manager{Browser: a.Browser, Accounts: a, LoginTimeout: time.Second}
	for _, interactive := range []bool{true, false} {
		if e := manager.Ensure(ctx, interactive, false, config.Credentials{}); e != nil {
			t.Fatal(e)
		}
	}
	s, e := a.Snapshot(ctx)
	if e != nil || len(s.Accounts) != 4 {
		t.Fatalf("snapshot=%+v error=%v", s, e)
	}
	if len(events()) != 0 {
		t.Fatalf("read-only commands sent updates: %v", events())
	}
}

func TestOnlyRenderedAccountStatus(t *testing.T) {
	for _, tt := range []struct {
		name    string
		options map[string]any
		status  string
	}{
		{"normal with hidden updating", map[string]any{"statusTemplates": true}, "ok"},
		{"updating with hidden normal", map[string]any{"statusTemplates": true, "updating": true}, "updating"},
		{"auth required with hidden updating", map[string]any{"statusTemplates": true, "authRequired": true}, "auth_required"},
		{"unknown with hidden updating", map[string]any{"statusTemplates": true, "unknown": true}, "unknown"},
		{"hidden status cell", map[string]any{"statusTemplates": true, "hiddenWholeStatus": true}, "unknown"},
		{"hidden duplicate class", map[string]any{"statusTemplates": true, "hiddenStatusCell": true}, "ok"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			a, events := setup(t, tt.options)
			s, e := a.Snapshot(context.Background())
			if e != nil {
				t.Fatal(e)
			}
			found := false
			for _, x := range s.Accounts {
				if x.ID == "alpha" {
					found = true
					if x.Status != tt.status || !x.Automatic {
						t.Fatalf("status=%s automatic=%t want=%s", x.Status, x.Automatic, tt.status)
					}
				}
			}
			if !found {
				t.Fatal("synthetic account absent")
			}
			if len(events()) != 0 {
				t.Fatal("reading status sent an update")
			}
		})
	}
}
