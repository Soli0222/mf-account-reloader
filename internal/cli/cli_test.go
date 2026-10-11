package cli

import (
	"bytes"
	"context"
	"github.com/Soli0222/mf-account-reloader/internal/config"
	"testing"
)

func TestCommands(t *testing.T) {
	for _, args := range [][]string{nil, {"run", "--config", "config.yaml"}, {"accounts", "list", "--config", "config.yaml"}, {"auth", "login", "--config", "config.yaml"}, {"auth", "check", "--config", "config.yaml"}, {"version"}} {
		if _, e := Parse(args); e != nil {
			t.Fatalf("%v: %v", args, e)
		}
	}
	in, e := Parse([]string{"run", "--wait=false", "--extra-account-id", "alpha", "--extra-account-id", "beta"})
	if e != nil || in.Overrides.Values["wait"] != "false" || len(in.Overrides.IDs) != 2 {
		t.Fatal(in, e)
	}
	for _, args := range [][]string{{"unknown"}, {"auth"}, {"run", "leftover"}, {"--config"}, {"--format", "bad"}} {
		if _, e := Parse(args); e == nil {
			t.Fatal("invalid accepted", args)
		}
	}
}
func TestHelpAndVersionWithoutBrowser(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"version"}} {
		var out, err bytes.Buffer
		code := Execute(context.Background(), args, &out, &err, config.Env(func(string) string { return "" }))
		if code != 0 || out.Len() == 0 || err.Len() > 0 {
			t.Fatalf("code=%d out=%s err=%s", code, &out, &err)
		}
	}
}
func TestOverallDeadline(t *testing.T) {
	var out, diagnostics bytes.Buffer
	code := Execute(context.Background(), []string{"run", "--auth-state-file", t.TempDir() + "/state/auth-state.json", "--browser-path", "/nonexistent-synthetic-browser", "--run-timeout", "1ns"}, &out, &diagnostics, func(string) string { return "" })
	if code != 5 {
		t.Fatalf("overall deadline code=%d output=%s", code, &out)
	}
}
