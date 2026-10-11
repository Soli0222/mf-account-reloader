package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/Soli0222/mf-account-reloader/internal/auth"
	"github.com/Soli0222/mf-account-reloader/internal/browser"
	"github.com/Soli0222/mf-account-reloader/internal/config"
	"github.com/Soli0222/mf-account-reloader/internal/failure"
	"github.com/Soli0222/mf-account-reloader/internal/moneyforward"
	"github.com/Soli0222/mf-account-reloader/internal/reloader"
	"github.com/Soli0222/mf-account-reloader/internal/state"
	"github.com/Soli0222/mf-account-reloader/internal/version"
)

const help = `mf-account-reloader [run] [options]
mf-account-reloader accounts list [options] [--format json|text]
mf-account-reloader auth login|check [options]
mf-account-reloader version

Options:
  --config FILE                 explicit YAML configuration
  --extra-account-id ID         repeatable; merged with configuration
  --extra-account-name NAME     repeatable; exact registered name
  --dry-run                     plan without update clicks
  --wait[=false]                 wait for retrieval completion
  --headless[=false]             login defaults to a visible browser
  --no-sandbox[=false]           use only where the container requires it
  --auto-login[=false]           allow one automatic login attempt
  --auth-state-file FILE        writable application authentication state
  --auth-seed-file FILE         read-only initial state
  --browser-path FILE           Chrome/Chromium executable
  --log-format json|text         diagnostic output on stderr
  --log-level debug|info|warn|error
  --navigation-timeout 30s --action-timeout 15s
  --login-timeout 5m --run-timeout 10m --poll-interval 10s
`

type list []string

func (l *list) String() string     { return "" }
func (l *list) Set(s string) error { *l = append(*l, s); return nil }

type Invocation struct {
	Command, ConfigPath, Format string
	DryRun                      bool
	Overrides                   config.Overrides
	Help                        bool
}

func Parse(args []string) (Invocation, error) {
	in := Invocation{Command: "run", Format: "json", Overrides: config.Overrides{Values: map[string]string{}}}
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		in.Command = args[0]
		args = args[1:]
		if in.Command == "accounts" || in.Command == "auth" {
			if len(args) == 0 {
				return in, failure.New(2, "subcommand is required")
			}
			in.Command += " " + args[0]
			args = args[1:]
		}
	}
	switch in.Command {
	case "run", "accounts list", "auth login", "auth check", "version":
	default:
		return in, failure.New(2, "unknown command")
	}
	fs := flag.NewFlagSet("mf-account-reloader", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&in.ConfigPath, "config", "", "configuration file")
	fs.StringVar(&in.Format, "format", "json", "result format")
	fs.BoolVar(&in.DryRun, "dry-run", false, "dry run")
	fs.BoolVar(&in.Help, "help", false, "help")
	fs.BoolVar(&in.Help, "h", false, "help")
	var ids, names list
	fs.Var(&ids, "extra-account-id", "account ID")
	fs.Var(&names, "extra-account-name", "account name")
	for _, k := range []string{"auth-state-file", "auth-seed-file", "browser-path", "log-level", "log-format", "navigation-timeout", "action-timeout", "login-timeout", "run-timeout", "poll-interval"} {
		fs.String(k, "", "override")
	}
	for _, k := range []string{"headless", "no-sandbox", "auto-login", "wait"} {
		fs.Bool(k, false, "override")
	}
	if e := fs.Parse(args); e != nil {
		return in, failure.New(2, "invalid command line; see --help")
	}
	if fs.NArg() != 0 {
		return in, failure.New(2, "unexpected positional arguments")
	}
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "format", "dry-run", "help", "h", "extra-account-id", "extra-account-name":
		default:
			in.Overrides.Values[f.Name] = f.Value.String()
		}
	})
	in.Overrides.IDs = ids
	in.Overrides.Names = names
	if in.Format != "json" && in.Format != "text" {
		return in, failure.New(2, "format must be json or text")
	}
	return in, nil
}
func Execute(ctx context.Context, args []string, stdout, stderr io.Writer, env config.Env) int {
	in, e := Parse(args)
	if e != nil {
		fmt.Fprintln(stderr, failure.Safe(e))
		return failure.Code(e)
	}
	if in.Help {
		fmt.Fprint(stdout, help)
		return 0
	}
	if in.Command == "version" {
		if json.NewEncoder(stdout).Encode(version.Info()) != nil {
			return 1
		}
		return 0
	}
	c, e := config.Load(in.ConfigPath, in.Overrides, env)
	if e != nil {
		fmt.Fprintln(stderr, e.Error())
		return 2
	}
	credentials, e := config.ReadCredentials(env)
	if e != nil {
		fmt.Fprintln(stderr, e.Error())
		return 2
	}
	if in.Command == "auth login" && !c.HeadlessExplicit {
		c.Browser.Headless = false
	}
	level := slog.LevelInfo
	_ = level.UnmarshalText([]byte(c.Log.Level))
	opts := &slog.HandlerOptions{Level: level}
	var handler slog.Handler
	if c.Log.Format == "text" {
		handler = slog.NewTextHandler(stderr, opts)
	} else {
		handler = slog.NewJSONHandler(stderr, opts)
	}
	logger := slog.New(handler)
	run, cancel := context.WithTimeout(ctx, time.Duration(c.Timeouts.Run))
	defer cancel()
	result, err := perform(run, in, c, credentials, stdout, logger)
	if run.Err() != nil {
		err = run.Err()
	}
	if in.Command == "run" {
		if err != nil {
			result.Error = failure.Safe(err)
		}
		if json.NewEncoder(stdout).Encode(result) != nil {
			return 1
		}
	}
	if err != nil {
		logger.Error(failure.Safe(err), "exitCode", failure.Code(err))
	}
	return failure.Code(err)
}
func perform(ctx context.Context, in Invocation, c config.Config, credentials config.Credentials, out io.Writer, logger *slog.Logger) (result reloader.Result, err error) {
	start := time.Now()
	result = reloader.Result{Bulk: "unknown", Extra: []reloader.Extra{}, Wait: c.Reload.Wait}
	defer func() { result.ElapsedSeconds = time.Since(start).Seconds() }()
	lock, e := state.Acquire(c.Auth.StateFile)
	if e != nil {
		return result, e
	}
	defer lock.Close()
	saved, exists, e := state.Load(c.Auth.StateFile, c.Auth.SeedFile)
	if e != nil {
		return result, e
	}
	_ = exists
	b, e := browser.Open(ctx, c)
	if e != nil {
		return result, e
	}
	defer b.Close()
	bound, done := b.Bound(ctx, time.Duration(c.Timeouts.Action))
	e = b.Restore(bound, saved)
	done()
	if e != nil {
		return result, e
	}
	adapter := moneyforward.New(b, time.Duration(c.Timeouts.Navigation), time.Duration(c.Timeouts.Action))
	manager := auth.Manager{Browser: b, Accounts: adapter, LoginTimeout: time.Duration(c.Timeouts.Login)}
	if in.Command == "auth login" {
		logger.Info("complete login in the dedicated browser and open /accounts")
	}
	if e = manager.Ensure(ctx, in.Command == "auth login", c.Auth.AutoLogin && in.Command != "auth check", credentials); e != nil {
		return result, e
	}
	// Only authenticated state is saved; rotation is retained even if a later selector fails.
	save := func() error {
		bc, cc := b.Bound(ctx, time.Duration(c.Timeouts.Action))
		defer cc()
		_, ok, e := adapter.Probe(bc)
		if e != nil || !ok {
			return nil
		}
		s, e := b.Capture(bc)
		if e != nil {
			return failure.New(1, "cannot capture authenticated state")
		}
		return state.Save(c.Auth.StateFile, s)
	}
	if e = save(); e != nil {
		return result, e
	}
	defer func() {
		if ctx.Err() == nil {
			if e := save(); e != nil && err == nil {
				err = e
			}
		}
	}()
	switch in.Command {
	case "auth login", "auth check":
		err = json.NewEncoder(out).Encode(map[string]bool{"authenticated": true})
		return result, err
	case "accounts list":
		s, e := adapter.Snapshot(ctx)
		if e != nil {
			return result, e
		}
		if in.Format == "json" {
			type listed struct {
				ID        string `json:"id"`
				Name      string `json:"name"`
				Status    string `json:"status"`
				CanUpdate bool   `json:"canUpdate"`
			}
			accounts := []listed{}
			for _, a := range s.Accounts {
				accounts = append(accounts, listed{a.ID, a.Name, a.Status, a.CanUpdate})
			}
			e = json.NewEncoder(out).Encode(accounts)
		} else {
			for _, a := range s.Accounts {
				_, e = fmt.Fprintf(out, "%s\t%s\t%s\t%t\n", a.ID, strings.ReplaceAll(strings.ReplaceAll(a.Name, "\n", " "), "\t", " "), a.Status, a.CanUpdate)
				if e != nil {
					break
				}
			}
		}
		return result, e
	case "run":
		return reloader.Run(ctx, adapter, reloader.Options{IDs: c.Reload.ExtraAccountIDs, Names: c.Reload.ExtraAccountNames, DryRun: in.DryRun, Wait: c.Reload.Wait, PollInterval: time.Duration(c.Reload.PollInterval)})
	}
	return result, failure.New(1, "unsupported invocation")
}
