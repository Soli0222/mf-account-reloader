package moneyforward

import (
	"context"
	_ "embed"
	"net/url"
	"time"

	"github.com/Soli0222/mf-account-reloader/internal/browser"
	"github.com/Soli0222/mf-account-reloader/internal/failure"
)

//go:embed dom.js
var dom string

const AccountsURL = "https://moneyforward.com/accounts"

type Adapter struct {
	Browser            *browser.Browser
	AccountsURL        string
	Navigation, Action time.Duration
	observedUpdating   map[string]bool
	bulkPageDirty      bool
}

func New(b *browser.Browser, nav, action time.Duration) *Adapter {
	return &Adapter{Browser: b, AccountsURL: AccountsURL, Navigation: nav, Action: action}
}
func SameAccountsURL(raw, expected string) bool {
	u, e := url.Parse(raw)
	v, f := url.Parse(expected)
	return e == nil && f == nil && u.User == nil && u.Scheme == v.Scheme && u.Host == v.Host && (u.Path == v.Path || u.Path == v.Path+"/")
}
func (a *Adapter) Probe(ctx context.Context) (Snapshot, bool, error) {
	c, cancel := a.Browser.Bound(ctx, a.Action)
	defer cancel()
	var s Snapshot
	u, e := a.Browser.URL(c)
	if e != nil {
		return s, false, e
	}
	if !SameAccountsURL(u, a.AccountsURL) {
		return s, false, nil
	}
	status, e := a.Browser.DocumentStatus(c)
	if e != nil {
		return s, false, e
	}
	if e = browser.ResponseError(status); e != nil {
		return s, false, e
	}
	e = a.Browser.Read(c, `function(arg){const adapter=`+dom+`;return adapter.snapshot()}`, nil, &s)
	if e == nil && s.Structure && !s.Login && !a.bulkPageDirty {
		a.observe(&s)
	}
	return s, s.Structure && !s.Login, e
}

// Retain evidence across refreshes and other account updates. A normal status
// alone may still describe the retrieval that preceded this request.
func (a *Adapter) observe(s *Snapshot) {
	if a.observedUpdating == nil {
		a.observedUpdating = map[string]bool{}
	}
	counts := map[string]int{}
	for _, x := range s.Accounts {
		counts[x.ID]++
	}
	for i := range s.Accounts {
		x := &s.Accounts[i]
		if x.ID == "" || x.Ambiguous || counts[x.ID] != 1 {
			continue
		}
		if x.Status == "updating" {
			a.observedUpdating[x.ID] = true
		}
		x.ObservedUpdating = a.observedUpdating[x.ID]
	}
}
func (a *Adapter) Snapshot(ctx context.Context) (Snapshot, error) {
	if a.bulkPageDirty {
		return a.Refresh(ctx)
	}
	s, ok, e := a.Probe(ctx)
	if e != nil {
		if failure.Code(e) == 3 {
			return s, e
		}
		if ctx.Err() != nil {
			return s, ctx.Err()
		}
		if a.Browser.Context.Err() != nil {
			return s, failure.New(1, "browser closed")
		}
		return s, failure.New(4, "cannot read account page")
	}
	if !ok {
		if s.Structure && !s.Login {
			return s, failure.New(4, "account page structure mismatch")
		}
		return s, failure.New(3, "account page is not authenticated or its structure is missing")
	}
	return s, nil
}
func (a *Adapter) Refresh(ctx context.Context) (Snapshot, error) {
	if e := a.Browser.Reload(ctx, a.AccountsURL, a.Navigation); e != nil {
		return Snapshot{}, e
	}
	// The bulk click page labels even excluded accounts as updating. Only a
	// freshly loaded document can supply evidence for extra decisions and --wait.
	a.bulkPageDirty = false
	c, cancel := context.WithTimeout(ctx, a.Navigation)
	defer cancel()
	for {
		s, ok, e := a.Probe(c)
		if e != nil {
			if failure.Code(e) == 3 {
				return s, e
			}
			if ctx.Err() != nil {
				return s, ctx.Err()
			}
			return s, failure.New(4, "cannot read account page")
		}
		if ok {
			return s, nil
		}
		if s.Login {
			return s, failure.New(3, "authentication expired")
		}
		u, e := a.Browser.URL(a.Browser.Context)
		if e != nil {
			return s, failure.New(1, "browser unavailable")
		}
		if !SameAccountsURL(u, a.AccountsURL) {
			return s, failure.New(3, "authentication expired")
		}
		if e = browser.Pause(c, 100*time.Millisecond); e != nil {
			if ctx.Err() != nil {
				return s, ctx.Err()
			}
			return s, failure.New(4, "account page structure mismatch")
		}
	}
}
func uniqueAccount(s Snapshot, id string) (Account, bool) {
	var result Account
	n := 0
	for _, x := range s.Accounts {
		if x.ID == id {
			result = x
			n++
		}
	}
	return result, n == 1
}
func (a *Adapter) Update(ctx context.Context, id *string) (string, error) {
	if id == nil {
		// Start a new bulk cycle, retaining only updates still active now.
		a.observedUpdating = nil
	}
	before, e := a.Snapshot(ctx)
	if e != nil {
		return "unknown", e
	}
	if id != nil {
		x, ok := uniqueAccount(before, *id)
		if !ok || x.Ambiguous {
			return "failed", failure.New(4, "selected account disappeared or became ambiguous")
		}
		if x.Status == "updating" {
			return "already_updating", nil
		}
		if !x.CanUpdate {
			return "failed", failure.New(4, "individual update is unavailable")
		}
		// This new request cannot reuse completion evidence from the bulk run.
		delete(a.observedUpdating, *id)
	} else {
		if before.BulkCount != 1 {
			return "failed", failure.New(4, "bulk update control is missing or ambiguous")
		}
		if !before.BulkEnabled {
			n := 0
			all := true
			for _, x := range before.Accounts {
				if x.Automatic && x.Status != "suspended" && x.Status != "auth_required" {
					n++
					all = all && x.Status == "updating"
				}
			}
			if n > 0 && all {
				return "already_updating", nil
			}
			return "unknown", failure.New(4, "bulk update is unavailable without evidence of an active update")
		}
	}
	a.Browser.BeginUpdate(id == nil)
	defer a.Browser.EndUpdate()
	if id == nil {
		a.bulkPageDirty = true
	}
	c, cancel := a.Browser.Bound(ctx, a.Action)
	var clicked struct {
		Clicked bool   `json:"clicked"`
		Error   string `json:"error"`
	}
	e = a.Browser.Call(c, `function(arg){const adapter=`+dom+`;return adapter.click(arg)}`, map[string]any{"id": id}, &clicked)
	cancel()
	if ctx.Err() != nil {
		return "unknown", ctx.Err()
	}
	if clicked.Error != "" {
		if id == nil {
			a.bulkPageDirty = false
		}
		return "failed", failure.New(4, "update control changed before click")
	}
	// A navigation can interrupt CDP while clicking. Never retry the operation.
	_ = e
	deadline, done := context.WithTimeout(ctx, a.Action)
	defer done()
	for {
		if a.Browser.Context.Err() != nil {
			return "unknown", failure.New(1, "browser closed during update")
		}
		if a.Browser.UnexpectedDialog() {
			return "failed", failure.New(4, "unexpected dialog was dismissed")
		}
		after, ok, pe := a.Probe(deadline)
		if pe != nil && failure.Code(pe) == 3 {
			return "failed", pe
		}
		if pe == nil && ok {
			if id == nil {
				transition := false
				for _, x := range after.Accounts {
					old, found := uniqueAccount(before, x.ID)
					if found && old.Status != "updating" && x.Status == "updating" {
						transition = true
					}
				}
				if after.Receipt != "" && after.Receipt != before.Receipt || transition {
					return "accepted", nil
				}
			} else {
				x, found := uniqueAccount(after, *id)
				// A redraw can temporarily remove identity links or duplicate rows/controls.
				// Keep observing within the action deadline; never repeat the click.
				if found && !x.Ambiguous {
					if x.Status == "failed" || x.Status == "auth_required" {
						return "failed", failure.New(4, "individual update reported failure")
					}
					if x.Status == "updating" {
						return "accepted", nil
					}
					// Disabling a control can precede a rejected request. A global
					// receipt also cannot identify this row; require its updating state.
				}
			}
		} else if pe == nil {
			u, _ := a.Browser.URL(a.Browser.Context)
			if !SameAccountsURL(u, a.AccountsURL) || after.Login {
				return "failed", failure.New(3, "authentication lost during update")
			}
		}
		if e = browser.Pause(deadline, 100*time.Millisecond); e != nil {
			if ctx.Err() != nil {
				return "unknown", ctx.Err()
			}
			return "unknown", failure.New(4, "update receipt could not be confirmed; request was not resent")
		}
	}
}
