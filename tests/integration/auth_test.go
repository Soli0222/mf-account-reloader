//go:build integration

package integration

import (
	"context"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Soli0222/mf-account-reloader/internal/auth"
	"github.com/Soli0222/mf-account-reloader/internal/browser"
	"github.com/Soli0222/mf-account-reloader/internal/config"
	"github.com/Soli0222/mf-account-reloader/internal/failure"
	"github.com/Soli0222/mf-account-reloader/internal/moneyforward"
	"github.com/Soli0222/mf-account-reloader/internal/state"
	"github.com/chromedp/cdproto"
	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/fetch"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

func newBrowser(t *testing.T, ctx context.Context) *browser.Browser {
	t.Helper()
	c := config.Defaults()
	c.Browser.NoSandbox = os.Getenv("MFR_TEST_NO_SANDBOX") == "true"
	c.Browser.ExecutablePath = os.Getenv("MFR_BROWSER_PATH")
	b, e := browser.Open(ctx, c)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(b.Close)
	return b
}

// Intercept every request. The fake HTTPS auth origins never contact the live service.
func intercept(t *testing.T, b *browser.Browser, handler func(string) (string, int, []*fetch.HeaderEntry)) {
	t.Helper()
	interceptRequests(t, b, func(r *network.Request) (string, int, []*fetch.HeaderEntry) { return handler(r.URL) })
}
func interceptRequests(t *testing.T, b *browser.Browser, handler func(*network.Request) (string, int, []*fetch.HeaderEntry)) {
	t.Helper()
	events := chromedp.Events(b.Context, fetch.RequestPaused)
	if _, e := chromedp.Call(b.Context, fetch.Enable, fetch.EnableParams{Patterns: []*fetch.RequestPattern{{URLPattern: "*"}}}); e != nil {
		t.Fatal(e)
	}
	go func() {
		for ev, e := range events {
			if e != nil {
				return
			}
			body, status, headers := handler(ev.Request)
			headers = append(headers, &fetch.HeaderEntry{Name: "Content-Type", Value: "text/html; charset=utf-8"})
			_, _ = chromedp.Call(b.Context, fetch.FulfillRequest, fetch.FulfillRequestParams{RequestID: ev.RequestID, ResponseCode: int64(status), ResponseHeaders: headers, Body: []byte(body)})
		}
	}()
}

const emptyAccounts = `<!doctype html><table id="account-table"></table><button>金融機関からのデータ一括更新</button>`

// Deterministically reproduce a navigation that destroys the CDP reply after
// submission. The unsent variant proves that a lost reply is not itself success.
type lostReplyBrowser struct {
	*browser.Browser
	kind   string
	unsent bool
	calls  int
}

func (b *lostReplyBrowser) Call(ctx context.Context, fn string, arg, out any) error {
	v, ok := arg.(map[string]string)
	if !ok || v["kind"] != b.kind {
		return b.Browser.Call(ctx, fn, arg, out)
	}
	b.calls++
	if !b.unsent {
		if e := b.Browser.Call(ctx, fn, arg, out); e != nil {
			return e
		}
	}
	return &cdproto.Error{Code: -32000, Message: "Execution context was destroyed."}
}

func TestCookieAndOriginState(t *testing.T) {
	ctx := context.Background()
	b := newBrowser(t, ctx)
	intercept(t, b, func(raw string) (string, int, []*fetch.HeaderEntry) {
		return `<script>window.before=localStorage.getItem('token');localStorage.setItem('rotation',location.hostname);</script>` + emptyAccounts, 200, nil
	})
	saved := state.Empty()
	saved.Cookies = []*network.CookieParam{{Name: "session", Value: "synthetic", Domain: "moneyforward.com", Path: "/", HTTPOnly: true, SameSite: network.CookieSameSiteLax}, {Name: "persistent", Value: "synthetic", Domain: "id.moneyforward.com", Path: "/", Expires: cdp.TimeSinceEpoch(time.Now().Add(time.Hour).Unix()), Secure: true, SameSite: network.CookieSameSiteStrict}}
	saved.Origins["https://moneyforward.com"] = map[string]string{"token": "synthetic-me"}
	saved.Origins["https://id.moneyforward.com"] = map[string]string{"token": "synthetic-id"}
	if e := b.Restore(b.Context, saved); e != nil {
		t.Fatal(e)
	}
	for _, origin := range []string{"https://id.moneyforward.com", "https://moneyforward.com"} {
		if e := b.Navigate(ctx, origin+"/accounts", testNavigationTimeout); e != nil {
			t.Fatal(e)
		}
		var before string
		if e := b.Call(b.Context, `function(){return window.before}`, nil, &before); e != nil {
			t.Fatal(e)
		}
		if before != saved.Origins[origin]["token"] {
			t.Fatalf("localStorage restored after application script or to wrong origin: %q", before)
		}
	}
	out, e := b.Capture(b.Context)
	if e != nil {
		t.Fatal(e)
	}
	if out.Origins["https://id.moneyforward.com"]["rotation"] != "id.moneyforward.com" || out.Origins["https://moneyforward.com"]["rotation"] != "moneyforward.com" {
		t.Fatalf("origin rotation lost: %+v", out.Origins)
	}
	if len(out.Cookies) != 2 {
		t.Fatalf("cookies=%+v", out.Cookies)
	}
	for _, c := range out.Cookies {
		if c.Name == "session" && c.Expires != 0 {
			t.Fatal("session cookie became persistent")
		}
		if c.Name == "persistent" && (c.Expires == 0 || c.SameSite != network.CookieSameSiteStrict) {
			t.Fatal("persistent attributes lost")
		}
	}
}
func TestAutomaticAuthentication(t *testing.T) {
	for _, scenario := range []string{"password", "otp", "otp-failure", "missing-otp-secret", "invalid-totp", "fake-origin", "account-selector", "captcha", "expired", "lost-email-reply", "lost-password-reply", "lost-otp-reply", "lost-selection-reply", "lost-unsent-reply", "delayed-redirect"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), testRunTimeout)
			defer cancel()
			b := newBrowser(t, ctx)
			var mu sync.Mutex
			logged := false
			requests := map[string]int{}
			intercept(t, b, func(raw string) (string, int, []*fetch.HeaderEntry) {
				u, _ := url.Parse(raw)
				mu.Lock()
				defer mu.Unlock()
				requests[u.Path]++
				redirect := func(to string) (string, int, []*fetch.HeaderEntry) {
					return "", 302, []*fetch.HeaderEntry{{Name: "Location", Value: to}}
				}
				form := func(input, next string) string {
					return `<form onsubmit="event.preventDefault();location.href='` + next + `'"><input ` + input + `><button id="submitto" type="submit">Sign in</button></form>`
				}
				switch u.Path {
				case "/accounts":
					if logged {
						return emptyAccounts, 200, nil
					}
					if scenario == "delayed-redirect" {
						time.Sleep(1200 * time.Millisecond)
					}
					if scenario == "expired" {
						return `<input type="password">`, 200, nil
					}
					if scenario == "fake-origin" {
						return redirect("https://moneyforward.com.example.org/sign_in")
					}
					return redirect("https://id.moneyforward.com/sign_in")
				case "/sign_in":
					if scenario == "captcha" {
						return `<div>CAPTCHA</div>`, 200, nil
					}
					if scenario == "account-selector" || scenario == "lost-selection-reply" {
						return `<button onclick="location.href='/email'">synthetic@example.invalid</button>`, 200, nil
					}
					return form(`name="mfid_user[email]"`, "https://id.moneyforward.com/password"), 200, nil
				case "/email":
					return form(`name="mfid_user[email]"`, "https://id.moneyforward.com/password"), 200, nil
				case "/password":
					if scenario == "delayed-redirect" {
						time.Sleep(500 * time.Millisecond)
					}
					next := "https://moneyforward.com/done"
					if strings.Contains(scenario, "otp") {
						next = "https://id.moneyforward.com/otp"
					}
					return form(`name="mfid_user[password]" type="password"`, next), 200, nil
				case "/otp":
					if scenario == "otp-failure" {
						return `<form onsubmit="event.preventDefault();fetch('/otp-rejected')"><input autocomplete="one-time-code"><button id="submitto" type="submit">Verify</button></form>`, 200, nil
					}
					return form(`autocomplete="one-time-code"`, "https://moneyforward.com/done"), 200, nil
				case "/done":
					logged = true
					return redirect(moneyforward.AccountsURL)
				default:
					return "", 200, nil
				}
			})
			adapter := moneyforward.New(b, testNavigationTimeout, testActionTimeout)
			m := auth.Manager{Browser: b, Accounts: adapter, LoginTimeout: testLoginTimeout}
			var lost *lostReplyBrowser
			if strings.HasPrefix(scenario, "lost-") {
				kind := strings.TrimSuffix(strings.TrimPrefix(scenario, "lost-"), "-reply")
				lost = &lostReplyBrowser{Browser: b, kind: kind}
				if kind == "unsent" {
					lost.kind, lost.unsent = "email", true
				}
				m.Browser = lost
			}
			creds := config.Credentials{Email: "synthetic@example.invalid", Password: "synthetic", TOTPSecret: "JBSWY3DPEHPK3PXP"}
			if scenario == "missing-otp-secret" {
				creds.TOTPSecret = ""
			}
			if scenario == "invalid-totp" {
				creds.TOTPSecret = "invalid***"
			}
			e := m.Ensure(ctx, false, scenario != "expired", creds)
			expected := 0
			if scenario == "otp-failure" || scenario == "missing-otp-secret" || scenario == "invalid-totp" || scenario == "fake-origin" || scenario == "captcha" || scenario == "expired" || scenario == "lost-unsent-reply" {
				expected = 3
			}
			if failure.Code(e) != expected {
				t.Fatalf("code=%d expected=%d: %v", failure.Code(e), expected, e)
			}
			if lost != nil && lost.calls != 1 {
				t.Fatalf("uncertain stage submitted %d times", lost.calls)
			}
			mu.Lock()
			defer mu.Unlock()
			if requests["/password"] > 1 || requests["/otp-rejected"] > 1 {
				t.Fatal("repeated credential submission", requests)
			}
		})
	}
}
func TestCancelClosesBrowser(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	b := newBrowser(t, ctx)
	cancel()
	select {
	case <-b.Context.Done():
	case <-time.After(time.Second):
		t.Fatal("browser context did not cancel")
	}
	b.Close()
}
func TestStateStoreInRuntime(t *testing.T) {
	dir := os.Getenv("MFR_TEST_STATE_DIR")
	if dir != "" {
		c, e := config.Load("", config.Overrides{}, os.Getenv)
		if e != nil || c.Auth.StateFile != "/state/auth-state.json" || !c.Browser.NoSandbox {
			t.Fatal("container defaults", c, e)
		}
	}
	if dir == "" {
		dir = t.TempDir()
	}
	path := dir + "/synthetic-state-test.json"
	defer os.Remove(path)
	defer os.Remove(path + ".lock")
	lock, e := state.Acquire(path)
	if e != nil {
		t.Fatal(e)
	}
	defer lock.Close()
	if e = state.Save(path, state.Empty()); e != nil {
		t.Fatal(e)
	}
	info, e := os.Stat(path)
	if e != nil || info.Mode().Perm() != 0600 {
		t.Fatal("state permissions", e)
	}
	if _, exists, e := state.Load(path, ""); e != nil || !exists {
		t.Fatal("state round trip", e)
	}
}
func TestEmptyAccountsAndStructureMismatch(t *testing.T) {
	for _, valid := range []bool{true, false} {
		t.Run(map[bool]string{true: "empty but valid", false: "missing structure"}[valid], func(t *testing.T) {
			b := newBrowser(t, context.Background())
			intercept(t, b, func(string) (string, int, []*fetch.HeaderEntry) {
				if valid {
					return emptyAccounts, 200, nil
				}
				return `<p>unrecognized account screen</p>`, 200, nil
			})
			a := moneyforward.New(b, testNavigationTimeout, testActionTimeout)
			if !valid {
				a.Navigation = time.Second // Deliberately test a structure deadline.
			}
			m := auth.Manager{Browser: b, Accounts: a, LoginTimeout: testLoginTimeout}
			e := m.Ensure(context.Background(), false, false, config.Credentials{})
			code := 0
			if !valid {
				code = 4
			}
			if failure.Code(e) != code {
				t.Fatalf("code=%d want=%d: %v", failure.Code(e), code, e)
			}
		})
	}
}

// Reproduce a session that works with the visible browser's identity but is
// rejected when a new headless browser sends a different product token.
func TestHeadlessSessionReuse(t *testing.T) {
	ctx := context.Background()
	file := t.TempDir() + "/state.json"
	first := newBrowser(t, ctx)
	intercept(t, first, func(string) (string, int, []*fetch.HeaderEntry) {
		return emptyAccounts, 200, []*fetch.HeaderEntry{{Name: "Set-Cookie", Value: "synthetic_session=valid; Secure; HttpOnly; Path=/; SameSite=Lax"}}
	})
	if e := first.Navigate(ctx, moneyforward.AccountsURL, testNavigationTimeout); e != nil {
		t.Fatal(e)
	}
	captured, e := first.Capture(first.Context)
	if e != nil {
		t.Fatal(e)
	}
	if e = state.Save(file, captured); e != nil {
		t.Fatal(e)
	}
	first.Close()
	saved, exists, e := state.Load(file, "")
	if e != nil || !exists {
		t.Fatal("saved state", e)
	}
	second := newBrowser(t, ctx)
	interceptRequests(t, second, func(request *network.Request) (string, int, []*fetch.HeaderEntry) {
		ua := ""
		cookie := ""
		for key, value := range request.Headers {
			text, ok := value.(string)
			if !ok {
				continue
			}
			if strings.EqualFold(key, "User-Agent") {
				ua = text
			}
			if strings.EqualFold(key, "Cookie") {
				cookie = text
			}
		}
		if !strings.Contains(ua, "Chrome/") || strings.Contains(ua, "HeadlessChrome/") {
			return "Forbidden", 403, nil
		}
		if !strings.Contains(cookie, "synthetic_session=valid") {
			return `<input type="password">`, 200, nil
		}
		return emptyAccounts, 200, nil
	})
	if e = second.Restore(second.Context, saved); e != nil {
		t.Fatal(e)
	}
	adapter := moneyforward.New(second, testNavigationTimeout, testActionTimeout)
	manager := auth.Manager{Browser: second, Accounts: adapter, LoginTimeout: testLoginTimeout}
	if e = manager.Ensure(ctx, false, false, config.Credentials{}); e != nil {
		t.Fatalf("headless replay rejected: %v", e)
	}
}

func TestDocumentResponseAuthentication(t *testing.T) {
	for _, status := range []int{401, 403, 429, 503} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			b := newBrowser(t, context.Background())
			var mu sync.Mutex
			requests := 0
			intercept(t, b, func(raw string) (string, int, []*fetch.HeaderEntry) {
				u, _ := url.Parse(raw)
				if u.Path == "/accounts" {
					mu.Lock()
					requests++
					mu.Unlock()
				}
				return emptyAccounts, status, nil
			})
			a := moneyforward.New(b, testNavigationTimeout, testActionTimeout)
			m := auth.Manager{Browser: b, Accounts: a, LoginTimeout: testLoginTimeout}
			auto := status == 403
			e := m.Ensure(context.Background(), false, auto, config.Credentials{Email: "synthetic@example.invalid", Password: "synthetic"})
			want := 1
			if status == 401 || status == 403 {
				want = 3
			}
			if failure.Code(e) != want {
				t.Fatalf("HTTP %d: code=%d want=%d error=%v", status, failure.Code(e), want, e)
			}
			if status == 403 {
				mu.Lock()
				count := requests
				mu.Unlock()
				if count != 1 {
					t.Fatal("denied document retried automatic login")
				}
			}
			if status == 401 || status == 403 {
				if _, ok, e := a.Probe(context.Background()); ok || failure.Code(e) != 3 {
					t.Fatal("denied markup considered authenticated")
				}
			}
		})
	}
}

func TestForbiddenSubframeDoesNotInvalidateSession(t *testing.T) {
	b := newBrowser(t, context.Background())
	intercept(t, b, func(raw string) (string, int, []*fetch.HeaderEntry) {
		u, _ := url.Parse(raw)
		if u.Path == "/denied" {
			return "Forbidden", 403, nil
		}
		return emptyAccounts + `<iframe src="/denied"></iframe>`, 200, nil
	})
	a := moneyforward.New(b, testNavigationTimeout, testActionTimeout)
	m := auth.Manager{Browser: b, Accounts: a, LoginTimeout: testLoginTimeout}
	if e := m.Ensure(context.Background(), false, false, config.Credentials{}); e != nil {
		t.Fatal("subframe affected top-level authentication", e)
	}
}

func TestInteractiveChallengeNavigationClearsDeniedStatus(t *testing.T) {
	b := newBrowser(t, context.Background())
	intercept(t, b, func(raw string) (string, int, []*fetch.HeaderEntry) {
		u, _ := url.Parse(raw)
		if u.RawQuery == "ready=1" {
			return emptyAccounts, 200, nil
		}
		return `<script>setTimeout(()=>location.href='/accounts?ready=1',150)</script>`, 403, nil
	})
	a := moneyforward.New(b, testNavigationTimeout, testActionTimeout)
	m := auth.Manager{Browser: b, Accounts: a, LoginTimeout: testLoginTimeout}
	if e := m.Ensure(context.Background(), true, false, config.Credentials{}); e != nil {
		t.Fatal("new document retained old denial status", e)
	}
}
