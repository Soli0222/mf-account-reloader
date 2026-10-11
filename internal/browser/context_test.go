package browser

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/Soli0222/mf-account-reloader/internal/failure"
)

func TestBoundPreservesDeadline(t *testing.T) {
	for _, source := range []string{"caller", "browser", "action", "expired caller"} {
		t.Run(source, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				callerTimeout, browserTimeout, actionTimeout := time.Hour, time.Hour, time.Hour
				switch source {
				case "caller":
					callerTimeout = time.Second
				case "browser":
					browserTimeout = time.Second
				case "action":
					actionTimeout = time.Second
				case "expired caller":
					callerTimeout = -time.Second
				}
				caller, stopCaller := context.WithTimeout(context.Background(), callerTimeout)
				defer stopCaller()
				type browserKey struct{}
				browserCtx, stopBrowser := context.WithTimeout(context.WithValue(context.Background(), browserKey{}, "session"), browserTimeout)
				defer stopBrowser()
				b := &Browser{Context: browserCtx}
				started := time.Now()
				ctx, stop := b.Bound(caller, actionTimeout)
				defer stop()
				// A blocked CDP call returns the bound context's error, not the
				// caller's error. Exercise that path without scheduling real Chrome.
				err := Pause(ctx, 2*time.Hour)
				if err != context.DeadlineExceeded || failure.Code(err) != 5 || failure.Safe(err) != "operation deadline exceeded" {
					t.Fatalf("deadline misclassified: %v (code %d)", err, failure.Code(err))
				}
				if time.Since(started) > time.Second {
					t.Fatal("operation exceeded the earliest deadline")
				}
				if ctx.Value(browserKey{}) != "session" {
					t.Fatal("lost browser context values")
				}
			})
		})
	}
}

func TestBoundPreservesCancellation(t *testing.T) {
	for _, source := range []string{"caller", "already canceled caller", "browser", "operation"} {
		t.Run(source, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				caller, stopCaller := context.WithCancel(context.Background())
				defer stopCaller()
				browserCtx, stopBrowser := context.WithCancel(context.Background())
				defer stopBrowser()
				b := &Browser{Context: browserCtx}
				if source == "already canceled caller" {
					stopCaller()
				}
				ctx, stop := b.Bound(caller, time.Hour)
				defer stop()
				if source == "already canceled caller" && ctx.Err() != context.Canceled {
					t.Fatal("already canceled caller left operation runnable")
				}
				switch source {
				case "caller":
					stopCaller()
				case "browser":
					stopBrowser()
				case "operation":
					stop()
				}
				if err := Pause(ctx, 2*time.Hour); err != context.Canceled || failure.Code(err) != 1 {
					t.Fatalf("cancellation misclassified: %v", err)
				}
				if source != "browser" && browserCtx.Err() != nil {
					t.Fatal("operation canceled the browser session")
				}
				if source == "operation" && caller.Err() != nil {
					t.Fatal("operation canceled its caller")
				}
			})
		})
	}
}
