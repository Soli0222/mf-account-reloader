package browser

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Soli0222/mf-account-reloader/internal/config"
	"github.com/Soli0222/mf-account-reloader/internal/failure"
	"github.com/Soli0222/mf-account-reloader/internal/state"
	"github.com/chromedp/cdproto"
	browserproto "github.com/chromedp/cdproto/browser"
	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/domstorage"
	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/cdproto/storage"
	"github.com/chromedp/chromedp"
)

type Browser struct {
	Context                 context.Context
	cancel, cancelAllocator context.CancelFunc
	profile                 string
	origins                 map[string]map[string]string
	mu                      sync.Mutex
	pending                 atomic.Int32
	unexpected              atomic.Bool
	documentStatuses        sync.Map
}

func Open(ctx context.Context, c config.Config) (*Browser, error) {
	dir, e := os.MkdirTemp("", "mfr-browser-")
	if e != nil {
		return nil, failure.New(1, "cannot create browser profile")
	}
	opts := append([]chromedp.ExecAllocatorOption{}, chromedp.DefaultExecAllocatorOptions[:]...)
	opts = append(opts, chromedp.UserDataDir(dir), chromedp.Flag("headless", c.Browser.Headless), chromedp.Flag("no-sandbox", c.Browser.NoSandbox), chromedp.Flag("disable-dev-shm-usage", false))
	if c.Browser.ExecutablePath != "" {
		opts = append(opts, chromedp.ExecPath(c.Browser.ExecutablePath))
	}
	alloc, ac := chromedp.NewExecAllocator(ctx, opts...)
	bc, cc := chromedp.NewContext(alloc, chromedp.WithLogf(func(string, ...any) {}), chromedp.WithErrorf(func(string, ...any) {}))
	b := &Browser{Context: bc, cancel: cc, cancelAllocator: ac, profile: dir, origins: map[string]map[string]string{}}
	// Initialize on the long-lived context before subscribing.
	if e = chromedp.Do(bc, chromedp.Func(func(c context.Context, t *chromedp.Target) error {
		if _, e := cdp.Call(c, t, network.Enable, network.EnableParams{}); e != nil {
			return e
		}
		if _, e := cdp.Call(c, t, domstorage.Enable, cdp.Empty{}); e != nil {
			return e
		}
		_, e := cdp.Call(c, t, page.Enable, page.EnableParams{})
		return e
	})); e != nil {
		b.Close()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, failure.New(1, "cannot start Chrome/Chromium")
	}
	// Use the installed browser's own Chrome identity in both display modes.
	// Only the HeadlessChrome product token changes; no browser/OS version is hardcoded.
	if c.Browser.Headless {
		version, err := chromedp.Call(bc, browserproto.GetVersion, cdp.Empty{})
		if err == nil {
			ua := strings.ReplaceAll(version.UserAgent, "HeadlessChrome/", "Chrome/")
			if ua != version.UserAgent {
				_, err = chromedp.Call(bc, emulation.SetUserAgentOverride, emulation.SetUserAgentOverrideParams{UserAgent: ua})
			}
		}
		if err != nil {
			b.Close()
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, failure.New(1, "cannot configure browser user agent")
		}
	}
	responses := chromedp.Events(bc, network.ResponseReceived)
	go func() {
		for ev, e := range responses {
			if e != nil {
				return
			}
			if ev.Type == network.ResourceTypeDocument && ev.LoaderID != "" {
				b.documentStatuses.Store(ev.LoaderID, int(ev.Response.Status))
			}
		}
	}()
	events := chromedp.Events(bc, page.JavascriptDialogOpening)
	go func() {
		for d, e := range events {
			if e != nil {
				return
			}
			msg := strings.Join(strings.Fields(d.Message), "")
			accept := b.pending.Load() != 0 && d.Type == page.DialogTypeConfirm && validConfirmation(msg, b.pending.Load())
			if !accept {
				b.unexpected.Store(true)
			}
			_, _ = chromedp.Call(bc, page.HandleJavaScriptDialog, page.HandleJavaScriptDialogParams{Accept: accept})
		}
	}()
	b.watchStorage()
	return b, nil
}
func validConfirmation(msg string, kind int32) bool {
	switch msg {
	case "金融機関からのデータ一括更新を行いますか？", "金融機関からのデータ一括更新を行いますか?":
		return kind == 1
	case "この口座を更新しますか？", "この口座を更新しますか?":
		return kind == 2
	}
	return false
}
func (b *Browser) Close() {
	b.cancel()
	b.cancelAllocator()
	// Chrome descendants can finish writing briefly after the parent exits.
	for attempt := 0; attempt < 50; attempt++ {
		if os.RemoveAll(b.profile) == nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}
func (b *Browser) Bound(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	c, cancel := context.WithTimeout(b.Context, d)
	stop := context.AfterFunc(ctx, cancel)
	return c, func() { stop(); cancel() }
}
func (b *Browser) Navigate(ctx context.Context, url string, d time.Duration) error {
	for attempt := 0; attempt < 2; attempt++ {
		c, cancel := b.Bound(ctx, d)
		response, e := chromedp.Run(c, chromedp.NavigateResponse(url))
		if e == nil && response != nil {
			if tree, err := chromedp.Call(c, page.GetFrameTree, cdp.Empty{}); err == nil && tree.FrameTree != nil && tree.FrameTree.Frame != nil {
				b.documentStatuses.Store(tree.FrameTree.Frame.LoaderID, int(response.Status))
			}
		}
		cancel()
		if e == nil {
			if response != nil {
				if err := ResponseError(int(response.Status)); err != nil {
					return err
				}
			}
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if b.Context.Err() != nil {
			return failure.New(1, "browser closed")
		}
		retry := errors.Is(e, context.DeadlineExceeded) || strings.Contains(e.Error(), "net::ERR_")
		if attempt == 1 || !retry {
			return failure.New(1, "navigation failed")
		}
		if e = Pause(ctx, 250*time.Millisecond); e != nil {
			return e
		}
	}
	return failure.New(1, "navigation failed")
}
func Pause(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Call passes data via CDP arguments, never through JS or selector concatenation.
func (b *Browser) Call(ctx context.Context, fn string, arg, out any) error {
	data, e := json.Marshal(arg)
	if e != nil {
		return e
	}
	global, e := chromedp.Call(ctx, runtime.Evaluate, runtime.EvaluateParams{Expression: "globalThis"})
	if e != nil {
		return e
	}
	if global.ExceptionDetails != nil {
		return failure.New(4, "browser script failed")
	}
	defer chromedp.Call(ctx, runtime.ReleaseObject, runtime.ReleaseObjectParams{ObjectID: global.Result.ObjectID})
	yes := true
	r, e := chromedp.Call(ctx, runtime.CallFunctionOn, runtime.CallFunctionOnParams{FunctionDeclaration: fn, ObjectID: global.Result.ObjectID, Arguments: []*runtime.CallArgument{{Value: data}}, ReturnByValue: &yes, AwaitPromise: &yes})
	if e != nil {
		return e
	}
	if r.ExceptionDetails != nil {
		return failure.New(4, "page structure could not be interpreted")
	}
	if out != nil {
		return json.Unmarshal(r.Result.Value, out)
	}
	return nil
}
func (b *Browser) URL(ctx context.Context) (string, error) {
	var url string
	e := b.Read(ctx, `function(){return location.href}`, nil, &url)
	return url, e
}

// NavigationInterrupted identifies CDP reads/calls whose execution context was
// replaced by navigation. It does not imply that a mutating call was not sent.
func NavigationInterrupted(err error) bool {
	var protocol *cdproto.Error
	if !errors.As(err, &protocol) || protocol.Code != -32000 {
		return false
	}
	for _, message := range []string{"Execution context was destroyed", "Cannot find context with specified id", "Could not find object with given id", "Inspected target navigated or closed"} {
		if strings.Contains(protocol.Message, message) {
			return true
		}
	}
	return false
}

// Read retries only read-only scripts interrupted by navigation, within ctx's
// deadline. Submissions and clicks must use Call and must never be retried here.
func (b *Browser) Read(ctx context.Context, fn string, arg, out any) error {
	for {
		e := b.Call(ctx, fn, arg, out)
		if !NavigationInterrupted(e) || b.Context.Err() != nil {
			return e
		}
		if e = Pause(ctx, 50*time.Millisecond); e != nil {
			return e
		}
	}
}
func (b *Browser) Restore(ctx context.Context, s state.State) error {
	b.mu.Lock()
	b.origins = cloneOrigins(s.Origins)
	if b.origins == nil {
		b.origins = map[string]map[string]string{}
	}
	b.mu.Unlock()
	return chromedp.Do(ctx, chromedp.Func(func(c context.Context, t *chromedp.Target) error {
		if len(s.Cookies) > 0 {
			if _, e := cdp.Call(c, t, network.SetCookies, network.SetCookiesParams{Cookies: s.Cookies}); e != nil {
				return failure.New(2, "cannot restore authentication cookies")
			}
		}
		// Run in the main world before page scripts at every navigation, origin separated.
		v, e := json.Marshal(s.Origins)
		if e != nil {
			return e
		}
		script := `(()=>{const origins=` + string(v) + `;const values=origins&&origins[location.origin];if(values&&sessionStorage.getItem('__mfr_state_restored')!=='1'){for(const [k,v] of Object.entries(values))localStorage.setItem(k,v);sessionStorage.setItem('__mfr_state_restored','1');}})()`
		_, e = cdp.Call(c, t, page.AddScriptToEvaluateOnNewDocument, page.AddScriptToEvaluateOnNewDocumentParams{Source: script})
		return e
	}))
}
func (b *Browser) Capture(ctx context.Context) (state.State, error) {
	s := state.Empty()
	e := chromedp.Do(ctx, chromedp.Func(func(c context.Context, t *chromedp.Target) error {
		cookies, e := cdp.Call(c, t, storage.GetCookies, storage.GetCookiesParams{})
		if e != nil {
			return e
		}
		for _, v := range cookies.Cookies {
			if !state.AllowedDomain(v.Domain) {
				continue
			}
			p := &network.CookieParam{Name: v.Name, Value: v.Value, Domain: v.Domain, Path: v.Path, Secure: v.Secure, HTTPOnly: v.HTTPOnly, SameSite: v.SameSite, Priority: v.Priority, SourceScheme: v.SourceScheme, SourcePort: v.SourcePort, PartitionKey: v.PartitionKey}
			if !v.Session && v.Expires > 0 {
				p.Expires = cdp.TimeSinceEpoch(v.Expires)
			}
			s.Cookies = append(s.Cookies, p)
		}
		return nil
	}))
	if e != nil {
		return s, e
	}
	var current struct {
		Origin string            `json:"origin"`
		Values map[string]string `json:"values"`
	}
	e = b.Call(ctx, `function(){return {origin:location.origin,values:Object.fromEntries(Object.entries(localStorage))}}`, nil, &current)
	if e != nil {
		return s, e
	}
	b.mu.Lock()
	s.Origins = cloneOrigins(b.origins)
	b.mu.Unlock()
	if state.AllowedOrigin(current.Origin) {
		s.Origins[current.Origin] = current.Values
	}
	return s, nil
}
func (b *Browser) RecordOrigin(ctx context.Context) error {
	var r struct {
		Origin string            `json:"origin"`
		Values map[string]string `json:"values"`
	}
	if e := b.Call(ctx, `function(){return {origin:location.origin,values:Object.fromEntries(Object.entries(localStorage))}}`, nil, &r); e != nil {
		return e
	}
	if state.AllowedOrigin(r.Origin) {
		b.mu.Lock()
		b.origins[r.Origin] = r.Values
		b.mu.Unlock()
	}
	return nil
}
func (b *Browser) BeginUpdate(bulk bool) {
	b.unexpected.Store(false)
	if bulk {
		b.pending.Store(1)
	} else {
		b.pending.Store(2)
	}
}
func (b *Browser) EndUpdate()             { b.pending.Store(0) }
func (b *Browser) UnexpectedDialog() bool { return b.unexpected.Load() }

func cloneOrigins(input map[string]map[string]string) map[string]map[string]string {
	out := map[string]map[string]string{}
	for origin, values := range input {
		out[origin] = map[string]string{}
		for k, v := range values {
			out[origin][k] = v
		}
	}
	return out
}
func (b *Browser) store(id *domstorage.StorageID, key, value, operation string) {
	if id == nil || !id.IsLocalStorage || !state.AllowedOrigin(id.SecurityOrigin) {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.origins[id.SecurityOrigin] == nil {
		b.origins[id.SecurityOrigin] = map[string]string{}
	}
	switch operation {
	case "clear":
		b.origins[id.SecurityOrigin] = map[string]string{}
	case "remove":
		delete(b.origins[id.SecurityOrigin], key)
	default:
		b.origins[id.SecurityOrigin][key] = value
	}
}
func (b *Browser) watchStorage() {
	added := chromedp.Events(b.Context, domstorage.DomStorageItemAdded)
	updated := chromedp.Events(b.Context, domstorage.DomStorageItemUpdated)
	removed := chromedp.Events(b.Context, domstorage.DomStorageItemRemoved)
	cleared := chromedp.Events(b.Context, domstorage.DomStorageItemsCleared)
	go func() {
		for v, e := range added {
			if e != nil {
				return
			}
			b.store(v.StorageID, v.Key, v.NewValue, "set")
		}
	}()
	go func() {
		for v, e := range updated {
			if e != nil {
				return
			}
			b.store(v.StorageID, v.Key, v.NewValue, "set")
		}
	}()
	go func() {
		for v, e := range removed {
			if e != nil {
				return
			}
			b.store(v.StorageID, v.Key, "", "remove")
		}
	}()
	go func() {
		for v, e := range cleared {
			if e != nil {
				return
			}
			b.store(v.StorageID, "", "", "clear")
		}
	}()
}

// DocumentStatus reads the main frame's current loader, so subframe failures and
// previous documents cannot contaminate authentication checks.
func (b *Browser) DocumentStatus(ctx context.Context) (int, error) {
	tree, e := chromedp.Call(ctx, page.GetFrameTree, cdp.Empty{})
	if e != nil {
		return 0, e
	}
	if tree.FrameTree == nil || tree.FrameTree.Frame == nil {
		return 0, nil
	}
	if status, ok := b.documentStatuses.Load(tree.FrameTree.Frame.LoaderID); ok {
		return status.(int), nil
	}
	return 0, nil
}
