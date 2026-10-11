package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

type Duration time.Duration

func (d *Duration) UnmarshalYAML(n *yaml.Node) error {
	v, e := time.ParseDuration(n.Value)
	if e != nil {
		return fmt.Errorf("invalid duration")
	}
	*d = Duration(v)
	return nil
}
func (d Duration) MarshalYAML() (any, error) { return time.Duration(d).String(), nil }

type Config struct {
	Auth struct {
		StateFile string `yaml:"stateFile"`
		SeedFile  string `yaml:"seedFile"`
		AutoLogin bool   `yaml:"autoLogin"`
	} `yaml:"auth"`
	Browser struct {
		ExecutablePath string `yaml:"executablePath"`
		Headless       bool   `yaml:"headless"`
		NoSandbox      bool   `yaml:"noSandbox"`
	} `yaml:"browser"`
	Reload struct {
		ExtraAccountIDs   []string `yaml:"extraAccountIds"`
		ExtraAccountNames []string `yaml:"extraAccountNames"`
		Wait              bool     `yaml:"wait"`
		PollInterval      Duration `yaml:"pollInterval"`
	} `yaml:"reload"`
	Timeouts struct {
		Navigation Duration `yaml:"navigation"`
		Action     Duration `yaml:"action"`
		Login      Duration `yaml:"login"`
		Run        Duration `yaml:"run"`
	} `yaml:"timeouts"`
	Log struct {
		Format string `yaml:"format"`
		Level  string `yaml:"level"`
	} `yaml:"log"`
	HeadlessExplicit bool `yaml:"-"`
}
type Overrides struct {
	Values     map[string]string
	IDs, Names []string
}
type Env func(string) string

func Defaults() Config {
	var c Config
	c.Reload.ExtraAccountIDs = []string{}
	c.Reload.ExtraAccountNames = []string{}
	c.Auth.StateFile = "./data/auth-state.json"
	c.Browser.Headless = true
	c.Reload.PollInterval = Duration(10 * time.Second)
	c.Timeouts.Navigation = Duration(30 * time.Second)
	c.Timeouts.Action = Duration(15 * time.Second)
	c.Timeouts.Login = Duration(5 * time.Minute)
	c.Timeouts.Run = Duration(10 * time.Minute)
	c.Log.Format = "json"
	c.Log.Level = "info"
	return c
}
func Load(path string, o Overrides, env Env) (Config, error) {
	c := Defaults()
	if explicit, ok := o.Values["config"]; ok {
		path = explicit
	} else if path == "" {
		path = env("MFR_CONFIG")
	}
	if path != "" {
		b, e := os.ReadFile(path)
		if e != nil {
			return c, fmt.Errorf("cannot read configuration file")
		}
		dec := yaml.NewDecoder(bytes.NewReader(b))
		dec.KnownFields(true)
		if e = dec.Decode(&c); e != nil {
			return c, fmt.Errorf("invalid YAML configuration")
		}
		var trailing any
		if e = dec.Decode(&trailing); e != io.EOF {
			return c, fmt.Errorf("configuration must contain one YAML document")
		}
		var presence struct {
			Browser struct {
				Headless *bool `yaml:"headless"`
			} `yaml:"browser"`
		}
		_ = yaml.Unmarshal(b, &presence)
		c.HeadlessExplicit = presence.Browser.Headless != nil
	}
	setters := map[string]*string{"auth-state-file": &c.Auth.StateFile, "auth-seed-file": &c.Auth.SeedFile, "browser-path": &c.Browser.ExecutablePath, "log-level": &c.Log.Level, "log-format": &c.Log.Format}
	names := map[string]string{"auth-state-file": "MFR_AUTH_STATE_FILE", "auth-seed-file": "MFR_AUTH_SEED_FILE", "browser-path": "MFR_BROWSER_PATH", "log-level": "MFR_LOG_LEVEL", "log-format": "MFR_LOG_FORMAT"}
	for k, p := range setters {
		if v := env(names[k]); v != "" {
			*p = v
		}
		if v, ok := o.Values[k]; ok {
			*p = v
		}
	}
	for _, s := range []struct {
		k, env string
		p      *bool
	}{{"headless", "MFR_HEADLESS", &c.Browser.Headless}, {"no-sandbox", "MFR_NO_SANDBOX", &c.Browser.NoSandbox}, {"auto-login", "MFR_AUTO_LOGIN", &c.Auth.AutoLogin}, {"wait", "MFR_WAIT", &c.Reload.Wait}} {
		v := env(s.env)
		if x, ok := o.Values[s.k]; ok {
			v = x
		}
		if v != "" {
			if v != "true" && v != "false" {
				return c, fmt.Errorf("invalid boolean for %s", s.k)
			}
			*s.p = v == "true"
			if s.k == "headless" {
				c.HeadlessExplicit = true
			}
		}
	}
	for _, s := range []struct {
		k string
		p *Duration
	}{{"navigation-timeout", &c.Timeouts.Navigation}, {"action-timeout", &c.Timeouts.Action}, {"login-timeout", &c.Timeouts.Login}, {"run-timeout", &c.Timeouts.Run}, {"poll-interval", &c.Reload.PollInterval}} {
		if v, ok := o.Values[s.k]; ok {
			d, e := time.ParseDuration(v)
			if e != nil {
				return c, fmt.Errorf("invalid duration for %s", s.k)
			}
			*s.p = Duration(d)
		}
	}
	for _, s := range []struct {
		env string
		p   *[]string
	}{{"MFR_EXTRA_ACCOUNT_IDS", &c.Reload.ExtraAccountIDs}, {"MFR_EXTRA_ACCOUNT_NAMES", &c.Reload.ExtraAccountNames}} {
		if v := env(s.env); v != "" {
			var a []string
			if json.Unmarshal([]byte(v), &a) != nil || !strings.HasPrefix(strings.TrimSpace(v), "[") {
				return c, fmt.Errorf("%s must be a JSON string array", s.env)
			}
			*s.p = a
		}
	}
	c.Reload.ExtraAccountIDs = Unique(append(c.Reload.ExtraAccountIDs, o.IDs...))
	accountNames := append(c.Reload.ExtraAccountNames, o.Names...)
	for i := range accountNames {
		accountNames[i] = strings.TrimSpace(accountNames[i])
	}
	c.Reload.ExtraAccountNames = Unique(accountNames)
	return c, c.Validate()
}
func Unique(a []string) []string {
	r := []string{}
	seen := map[string]bool{}
	for _, s := range a {
		if !seen[s] {
			r = append(r, s)
			seen[s] = true
		}
	}
	return r
}
func (c Config) Validate() error {
	if c.Auth.StateFile == "" {
		return fmt.Errorf("auth.stateFile is required")
	}
	if c.Auth.SeedFile != "" && c.Auth.StateFile == c.Auth.SeedFile {
		return fmt.Errorf("state and seed must be different files")
	}
	if c.Reload.PollInterval <= 0 || c.Timeouts.Action <= 0 || c.Timeouts.Navigation <= 0 || c.Timeouts.Login <= 0 || c.Timeouts.Run <= 0 {
		return fmt.Errorf("durations must be positive")
	}
	if c.Log.Format != "json" && c.Log.Format != "text" {
		return fmt.Errorf("log format must be json or text")
	}
	switch c.Log.Level {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("invalid log level")
	}
	for _, a := range [][]string{c.Reload.ExtraAccountIDs, c.Reload.ExtraAccountNames} {
		for _, s := range a {
			if strings.TrimSpace(s) == "" {
				return fmt.Errorf("empty account selector")
			}
		}
	}
	return nil
}

type Credentials struct{ Email, Password, TOTPSecret string }

func ReadCredentials(env Env) (Credentials, error) {
	var c Credentials
	for _, s := range []struct {
		key string
		p   *string
	}{{"MFR_EMAIL", &c.Email}, {"MFR_PASSWORD", &c.Password}, {"MFR_TOTP_SECRET", &c.TOTPSecret}} {
		v, f := env(s.key), env(s.key+"_FILE")
		if v != "" && f != "" {
			return c, fmt.Errorf("%s and its _FILE are mutually exclusive", s.key)
		}
		if f != "" {
			b, e := os.ReadFile(f)
			if e != nil {
				return c, fmt.Errorf("cannot read %s_FILE", s.key)
			}
			v = strings.TrimRight(string(b), "\r\n")
		}
		*s.p = v
	}
	return c, nil
}
