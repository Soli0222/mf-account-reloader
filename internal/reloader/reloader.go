package reloader

import (
	"context"
	"time"

	"github.com/Soli0222/mf-account-reloader/internal/browser"
	"github.com/Soli0222/mf-account-reloader/internal/failure"
	"github.com/Soli0222/mf-account-reloader/internal/moneyforward"
)

type Page interface {
	Snapshot(context.Context) (moneyforward.Snapshot, error)
	Refresh(context.Context) (moneyforward.Snapshot, error)
	Update(context.Context, *string) (string, error)
}
type Extra struct {
	Index  int    `json:"index"`
	Status string `json:"status"`
}
type Result struct {
	Bulk           string  `json:"bulk"`
	Extra          []Extra `json:"extra"`
	ElapsedSeconds float64 `json:"elapsedSeconds"`
	Wait           bool    `json:"wait"`
	Excluded       int     `json:"excluded"`
	Error          string  `json:"error,omitempty"`
}
type Options struct {
	IDs, Names                []string
	DryRun, Wait              bool
	PollInterval, MinInterval time.Duration
	Pause                     func(context.Context, time.Duration) error
}

func Run(ctx context.Context, p Page, o Options) (result Result, err error) {
	start := time.Now()
	result = Result{Bulk: "unknown", Extra: []Extra{}, Wait: o.Wait}
	defer func() {
		result.ElapsedSeconds = time.Since(start).Seconds()
		if err != nil {
			result.Error = failure.Safe(err)
		}
	}()
	pause := o.Pause
	if pause == nil {
		pause = browser.Pause
	}
	if o.MinInterval <= 0 {
		o.MinInterval = time.Second
	}
	s, e := p.Snapshot(ctx)
	if e != nil {
		return result, e
	}
	selected, e := moneyforward.Resolve(s.Accounts, o.IDs, o.Names)
	if e != nil {
		return result, e
	}
	if s.BulkCount != 1 {
		return result, failure.New(4, "bulk update control is missing or ambiguous")
	}
	for i := range selected {
		result.Extra = append(result.Extra, Extra{i, "planned"})
	}
	targets := map[string]bool{}
	unknownTargets := false
	for _, x := range s.Accounts {
		if !x.Automatic || x.Status == "suspended" || x.Status == "auth_required" {
			result.Excluded++
			continue
		}
		if x.ID == "" || x.Ambiguous || x.Status == "unknown" {
			unknownTargets = true
		}
		if targets[x.ID] {
			unknownTargets = true
		}
		targets[x.ID] = true
	}
	if o.Wait && unknownTargets {
		return result, failure.New(4, "bulk completion targets cannot be determined")
	}
	if o.DryRun {
		result.Bulk = "planned"
		return result, nil
	}
	result.Bulk, e = p.Update(ctx, nil)
	if e != nil {
		return result, e
	}
	var partial error
	for i, x := range selected {
		if e = pause(ctx, o.MinInterval); e != nil {
			return result, e
		}
		if _, e = p.Refresh(ctx); e != nil {
			return result, e
		}
		result.Extra[i].Status, e = p.Update(ctx, &x.ID)
		if e != nil {
			if failure.Code(e) == 3 || failure.Code(e) == 5 || failure.Code(e) == 1 {
				return result, e
			}
			partial = e
		}
		if e == nil {
			targets[x.ID] = true
		}
	}
	if !o.Wait {
		return result, partial
	}
	// Reconcile the wait set with the post-operation snapshot, including newly visible linked rows.
	after, e := p.Snapshot(ctx)
	if e != nil {
		return result, e
	}
	seenAfter := map[string]bool{}
	result.Excluded = 0
	for _, x := range after.Accounts {
		if !x.Automatic || x.Status == "suspended" || x.Status == "auth_required" {
			result.Excluded++
			continue
		}
		if x.ID == "" || x.Ambiguous || seenAfter[x.ID] {
			return result, failure.New(4, "post-update completion targets cannot be determined")
		}
		seenAfter[x.ID] = true
		targets[x.ID] = true
	}
	for {
		s, e = p.Refresh(ctx)
		if e != nil {
			return result, e
		}
		done := true
		current := map[string]moneyforward.Account{}
		for _, x := range s.Accounts {
			if _, exists := current[x.ID]; exists && targets[x.ID] {
				return result, failure.New(4, "completion target became ambiguous")
			}
			current[x.ID] = x
		}
		for id := range targets {
			x, exists := current[id]
			if !exists {
				return result, failure.New(4, "completion target disappeared")
			}
			switch x.Status {
			case "updating":
				done = false
			case "ok":
			default:
				return result, failure.New(4, "completion target failed or has an unknown state")
			}
		}
		if done {
			if partial == nil {
				result.Bulk = "completed"
			}
			for i := range result.Extra {
				if result.Extra[i].Status == "accepted" || result.Extra[i].Status == "already_updating" {
					result.Extra[i].Status = "completed"
				}
			}
			return result, partial
		}
		if e = pause(ctx, o.PollInterval); e != nil {
			return result, e
		}
	}
}
