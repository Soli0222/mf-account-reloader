package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func env(m map[string]string) Env { return func(k string) string { return m[k] } }
func file(t *testing.T, text string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	if e := os.WriteFile(p, []byte(text), 0600); e != nil {
		t.Fatal(e)
	}
	return p
}
func TestPriority(t *testing.T) {
	p := file(t, "browser:\n  headless: true\n  executablePath: yaml\nreload:\n  wait: true\n  extraAccountIds: [yaml, same]\nlog:\n  level: debug\n")
	c, e := Load(p, Overrides{Values: map[string]string{"headless": "false", "wait": "false", "browser-path": "cli"}, IDs: []string{"cli", "same"}}, env(map[string]string{"MFR_BROWSER_PATH": "env", "MFR_LOG_LEVEL": "warn"}))
	if e != nil {
		t.Fatal(e)
	}
	if c.Browser.Headless || c.Reload.Wait || c.Browser.ExecutablePath != "cli" || c.Log.Level != "warn" {
		t.Fatalf("wrong priority: %+v", c)
	}
	if !reflect.DeepEqual(c.Reload.ExtraAccountIDs, []string{"yaml", "same", "cli"}) {
		t.Fatal(c.Reload.ExtraAccountIDs)
	}
	c, e = Load(p, Overrides{}, env(map[string]string{"MFR_EXTRA_ACCOUNT_IDS": "[]", "MFR_EXTRA_ACCOUNT_NAMES": `["name, comma","name, comma"]`}))
	if e != nil || len(c.Reload.ExtraAccountIDs) != 0 || !reflect.DeepEqual(c.Reload.ExtraAccountNames, []string{"name, comma"}) {
		t.Fatalf("empty/JSON arrays: %+v %v", c, e)
	}
	if time.Duration(c.Timeouts.Action) != 15*time.Second {
		t.Fatal("flag defaults overrode YAML/defaults")
	}
}
func TestInvalidConfiguration(t *testing.T) {
	for _, v := range []string{"unknown: value", "timeouts:\n  run: invalid", "timeouts:\n  run: 0s", "browser:\n  headless: nope", "auth:\n  password: secret", "reload:\n  extraAccountIds: [\"\"]", "log:\n  format: invalid", "---\nlog:\n  level: info\n---\nlog:\n  level: debug"} {
		if _, e := Load(file(t, v), Overrides{}, env(nil)); e == nil {
			t.Fatalf("accepted %q", v)
		}
	}
	if _, e := Load("does-not-exist.yaml", Overrides{}, env(nil)); e == nil {
		t.Fatal("missing explicit config accepted")
	}
	for _, v := range []string{"a,b", "null", "[1]", "{}"} {
		if _, e := Load("", Overrides{}, env(map[string]string{"MFR_EXTRA_ACCOUNT_NAMES": v})); e == nil {
			t.Fatalf("accepted non-array %q", v)
		}
	}
}
func TestSecretFiles(t *testing.T) {
	p := file(t, "password with spaces\n")
	c, e := ReadCredentials(env(map[string]string{"MFR_PASSWORD_FILE": p}))
	if e != nil || c.Password != "password with spaces" {
		t.Fatal(c, e)
	}
	_, e = ReadCredentials(env(map[string]string{"MFR_PASSWORD_FILE": p, "MFR_PASSWORD": "secret"}))
	if e == nil {
		t.Fatal("secret conflict accepted")
	}
	_, e = ReadCredentials(env(map[string]string{"MFR_TOTP_SECRET_FILE": "missing"}))
	if e == nil {
		t.Fatal("missing secret file accepted")
	}
}
func TestExampleMatchesDefaults(t *testing.T) {
	c, e := Load("../../config.example.yaml", Overrides{}, env(nil))
	if e != nil {
		t.Fatal(e)
	}
	c.HeadlessExplicit = false
	if !reflect.DeepEqual(c, Defaults()) {
		t.Fatalf("example differs from defaults: %+v", c)
	}
}
func TestExplicitEmptyConfigAndOpaqueID(t *testing.T) {
	c, e := Load("", Overrides{Values: map[string]string{"config": ""}, IDs: []string{" opaque ", "opaque"}}, env(map[string]string{"MFR_CONFIG": "missing.yaml"}))
	if e != nil || len(c.Reload.ExtraAccountIDs) != 2 || c.Reload.ExtraAccountIDs[0] != " opaque " {
		t.Fatal(c, e)
	}
}
func TestContainerDefaultsAndExplicitConfig(t *testing.T) {
	c, e := Load("", Overrides{}, env(map[string]string{"MFR_CONFIG": "../../config.container.yaml"}))
	if e != nil || c.Auth.StateFile != "/state/auth-state.json" || !c.Browser.NoSandbox || c.Browser.ExecutablePath != "/usr/bin/chromium" {
		t.Fatal(c, e)
	}
	custom := file(t, "browser:\n  noSandbox: false\nauth:\n  stateFile: ./different/state.json\n")
	c, e = Load(custom, Overrides{}, env(map[string]string{"MFR_CONFIG": "../../config.container.yaml"}))
	if e != nil || c.Browser.NoSandbox || c.Auth.StateFile != "./different/state.json" {
		t.Fatal("image defaults overwrote explicit config", c, e)
	}
}
