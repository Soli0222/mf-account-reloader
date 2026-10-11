//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/Soli0222/mf-account-reloader/internal/browser"
	"github.com/Soli0222/mf-account-reloader/internal/failure"
	"github.com/chromedp/cdproto/fetch"
)

func TestReadSurvivesNavigation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), testRunTimeout)
	defer cancel()
	b := newBrowser(t, ctx)
	intercept(t, b, func(string) (string, int, []*fetch.HeaderEntry) { return `<!doctype html><p>synthetic</p>`, 200, nil })
	if e := b.Navigate(ctx, "https://moneyforward.com/before", testNavigationTimeout); e != nil {
		t.Fatal(e)
	}
	type result struct {
		path string
		err  error
	}
	done := make(chan result, 1)
	go func() {
		c, stop := b.Bound(ctx, testNavigationTimeout)
		defer stop()
		var path string
		e := b.Read(c, `function(){if(location.pathname==='/before'){window.readStarted=true;return new Promise(()=>{})}return location.pathname}`, nil, &path)
		done <- result{path, e}
	}()
	// Navigate only once the pending read is executing in the old document.
	for {
		c, stop := b.Bound(ctx, testActionTimeout)
		var started bool
		e := b.Call(c, `function(){return window.readStarted===true}`, nil, &started)
		stop()
		if e != nil {
			t.Fatal(e)
		}
		if started {
			break
		}
		if e := browser.Pause(ctx, 10*time.Millisecond); e != nil {
			t.Fatal(e)
		}
	}
	if e := b.Navigate(ctx, "https://moneyforward.com/after", testNavigationTimeout); e != nil {
		t.Fatal(e)
	}
	select {
	case r := <-done:
		if r.err != nil || r.path != "/after" {
			t.Fatalf("interrupted read: %+v", r)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

func TestReadDoesNotRetryScriptFailures(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), testRunTimeout)
	defer cancel()
	b := newBrowser(t, ctx)
	c, stop := b.Bound(ctx, testActionTimeout)
	defer stop()
	e := b.Read(c, `function(){window.attempts=(window.attempts||0)+1;throw new Error('synthetic failure')}`, nil, nil)
	if failure.Code(e) != 4 {
		t.Fatalf("script failure hidden: %v", e)
	}
	var attempts int
	if e := b.Call(c, `function(){return window.attempts}`, nil, &attempts); e != nil || attempts != 1 {
		t.Fatalf("script retried: attempts=%d error=%v", attempts, e)
	}
}
