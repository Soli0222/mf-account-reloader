package auth

import (
	"context"
	"errors"
	"net/url"
	"time"

	"github.com/Soli0222/mf-account-reloader/internal/browser"
	"github.com/Soli0222/mf-account-reloader/internal/config"
	"github.com/Soli0222/mf-account-reloader/internal/failure"
	"github.com/Soli0222/mf-account-reloader/internal/moneyforward"
	"github.com/pquerna/otp/totp"
)

type loginBrowser interface {
	Bound(context.Context, time.Duration) (context.Context, context.CancelFunc)
	Read(context.Context, string, any, any) error
	Call(context.Context, string, any, any) error
	Navigate(context.Context, string, time.Duration) error
	RecordOrigin(context.Context) error
	UnexpectedDialog() bool
}

type Manager struct {
	Browser      loginBrowser
	Accounts     *moneyforward.Adapter
	LoginTimeout time.Duration
	Now          func() time.Time
}

func (m *Manager) Ensure(ctx context.Context, interactive, auto bool, credentials config.Credentials) error {
	_, e := m.Accounts.Refresh(ctx)
	if e == nil {
		return nil
	}
	if failure.Code(e) != 3 {
		return e
	}
	var denied *browser.AccessDeniedError
	if errors.As(e, &denied) && denied.Status == 403 && !interactive {
		return e
	}
	var ok bool
	if !interactive && !auto {
		return failure.New(3, "authentication required; run auth login locally")
	}
	login, done := context.WithTimeout(ctx, m.LoginTimeout)
	defer done()
	if interactive {
		for {
			_, ok, e = m.Accounts.Probe(login)
			if e != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				var denied *browser.AccessDeniedError
				if !errors.As(e, &denied) {
					return failure.New(3, "interactive login browser is unavailable")
				}
			}
			if ok {
				return nil
			}
			if e = browser.Pause(login, 250*time.Millisecond); e != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				return failure.New(3, "interactive login deadline exceeded")
			}
		}
	}
	e = m.Auto(login, credentials)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return e
}

// Inspect and submit only known login states, never retry a submitted credential.
const inspect = `function(){
 const visible=e=>e.getClientRects().length>0&&getComputedStyle(e).visibility!=='hidden';
 const pick=s=>[...document.querySelectorAll(s)].filter(visible).length;
 return {url:location.href,ready:document.readyState!=='loading',email:pick('input[name="mfid_user[email]"]'),password:pick('input[name="mfid_user[password]"],input[type="password"]'),otp:pick('input[autocomplete="one-time-code"],input[name*="otp"],input[name*="code"]'),blocked:!!document.querySelector('iframe[src*="captcha"],#captcha')||/CAPTCHA|追加同意|SMSコード|メール.*コード/.test(document.body?.innerText||'')};
}`
const submit = `function(arg){
 if(location.href!==arg.url||!['https://moneyforward.com','https://id.moneyforward.com'].includes(location.origin))return false;
 const visible=e=>e.getClientRects().length>0&&getComputedStyle(e).visibility!=='hidden'&&!e.disabled;
 const selectors={email:'input[name="mfid_user[email]"]',password:'input[name="mfid_user[password]"],input[type="password"]',otp:'input[autocomplete="one-time-code"],input[name*="otp"],input[name*="code"]'};
 if(arg.kind==='selection'){
  const xs=[...document.querySelectorAll('button,a')].filter(visible).filter(e=>[arg.value,'メールアドレスでログイン','Sign in with email','マネーフォワード MEへ戻る','マネーフォワード ME'].includes((e.innerText||'').trim()));
  if(xs.length!==1)return false;xs[0].click();return true;
 }
 const inputs=[...document.querySelectorAll(selectors[arg.kind])].filter(visible);if(inputs.length!==1)return false;
 const i=inputs[0];const root=i.form||document;
 const buttons=[...root.querySelectorAll('#submitto,button[type="submit"],input[type="submit"]')].filter(visible);
 const unique=[...new Set(buttons)];if(unique.length!==1)return false;
 const set=Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set;set.call(i,arg.value);i.dispatchEvent(new Event('input',{bubbles:true}));i.dispatchEvent(new Event('change',{bubbles:true}));unique[0].click();return true;
}`

type formState struct {
	URL                  string
	Ready, Blocked       bool
	Email, Password, OTP int
}

func uncertainSubmission(err error) bool {
	return browser.NavigationInterrupted(err) || errors.Is(err, context.DeadlineExceeded)
}

func trusted(raw string) bool {
	u, e := url.Parse(raw)
	return e == nil && u.User == nil && u.Scheme == "https" && (u.Host == "moneyforward.com" || u.Host == "id.moneyforward.com")
}
func (m *Manager) Auto(ctx context.Context, c config.Credentials) error {
	if c.Email == "" || c.Password == "" {
		return failure.New(3, "automatic login needs email and password")
	}
	now := m.Now
	if now == nil {
		now = time.Now
	}
	used := map[string]bool{}
	returnedToME := false
	transitions := 0
	for transitions < 8 {
		if m.Browser.UnexpectedDialog() {
			return failure.New(3, "unexpected authentication dialog")
		}
		if ctx.Err() != nil {
			return failure.New(3, "automatic login deadline exceeded")
		}
		_, ok, e := m.Accounts.Probe(ctx)
		if e != nil {
			return failure.New(3, "cannot inspect login state")
		}
		if ok {
			return nil
		}
		bound, cancel := m.Browser.Bound(ctx, m.Accounts.Action)
		var f formState
		e = m.Browser.Read(bound, inspect, nil, &f)
		if e != nil {
			cancel()
			return failure.New(3, "cannot inspect authentication form")
		}
		if !trusted(f.URL) {
			cancel()
			return failure.New(3, "unexpected authentication origin")
		}
		if !f.Ready {
			cancel()
			if e = browser.Pause(ctx, 100*time.Millisecond); e != nil {
				return failure.New(3, "automatic login deadline exceeded")
			}
			continue
		}
		_ = m.Browser.RecordOrigin(bound)
		if f.Blocked {
			cancel()
			return failure.New(3, "interactive authentication is required")
		}
		kind, value := "", ""
		switch {
		case f.OTP > 0:
			if f.OTP != 1 || used["otp"] || c.TOTPSecret == "" {
				cancel()
				return failure.New(3, "OTP requires interactive login")
			}
			value, e = totp.GenerateCode(c.TOTPSecret, now())
			if e != nil {
				cancel()
				return failure.New(3, "cannot generate TOTP")
			}
			kind = "otp"
		case f.Password > 0:
			if f.Password != 1 || used["password"] {
				cancel()
				return failure.New(3, "password login failed or requires another interaction")
			}
			kind, value = "password", c.Password
		case f.Email > 0:
			if f.Email != 1 || used["email"] {
				cancel()
				return failure.New(3, "email login failed")
			}
			kind, value = "email", c.Email
		default:
			uParsed, _ := url.Parse(f.URL)
			if !returnedToME && used["password"] && (uParsed.Host == "id.moneyforward.com" && uParsed.Path == "/" || uParsed.Host == "moneyforward.com" && uParsed.Path == "/") {
				cancel()
				returnedToME = true
				transitions++
				if e = m.Browser.Navigate(ctx, m.Accounts.AccountsURL, m.Accounts.Navigation); e != nil {
					return failure.New(3, "cannot return to account page")
				}
				continue
			}
			if used["selection"] {
				cancel()
				if e = browser.Pause(ctx, 100*time.Millisecond); e != nil {
					return failure.New(3, "repeated account selection requires interactive login")
				}
				continue
			}
			var selection bool
			e = m.Browser.Call(bound, submit, map[string]string{"kind": "selection", "value": c.Email, "url": f.URL}, &selection)
			cancel()
			if e != nil && !uncertainSubmission(e) {
				return failure.New(3, "account selection could not be confirmed")
			}
			if e == nil && selection || uncertainSubmission(e) {
				transitions++
				if used["selection"] {
					return failure.New(3, "repeated account selection")
				}
				used["selection"] = true
				_ = browser.Pause(ctx, 300*time.Millisecond)
				continue
			}
			if e = browser.Pause(ctx, 250*time.Millisecond); e != nil {
				return failure.New(3, "automatic login needs interactive authentication")
			}
			continue
		}
		var submitted bool
		e = m.Browser.Call(bound, submit, map[string]string{"kind": kind, "value": value, "url": f.URL}, &submitted)
		cancel()
		if e != nil && !uncertainSubmission(e) || e == nil && !submitted {
			return failure.New(3, "authentication submission could not be confirmed")
		}
		// Navigation can destroy the reply after a successful click. Treat the
		// stage as used, then confirm advancement by reading; never submit twice.
		used[kind] = true
		transitions++
		// Wait for the submitted form to disappear. A failed unchanged form is not submitted twice.
		until, stop := context.WithTimeout(ctx, m.Accounts.Action)
		for {
			if e = browser.Pause(until, 100*time.Millisecond); e != nil {
				stop()
				return failure.New(3, "authentication step did not advance")
			}
			_, ok, _ = m.Accounts.Probe(until)
			if ok {
				stop()
				return nil
			}
			bc, cc := m.Browser.Bound(until, m.Accounts.Action)
			var next formState
			e = m.Browser.Read(bc, inspect, nil, &next)
			cc()
			if e == nil && !trusted(next.URL) {
				stop()
				return failure.New(3, "unexpected authentication origin")
			}
			if e == nil && next.Ready && (kind == "email" && next.Email == 0 || kind == "password" && next.Password == 0 || kind == "otp" && next.OTP == 0) {
				break
			}
		}
		stop()
	}
	return failure.New(3, "authentication transition limit reached")
}
