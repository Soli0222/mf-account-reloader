package state

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Soli0222/mf-account-reloader/internal/failure"
	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/network"
)

func fixture() State {
	s := Empty()
	s.Cookies = []*network.CookieParam{{Name: "session", Value: "synthetic", Domain: "moneyforward.com", Path: "/", HTTPOnly: true, SameSite: network.CookieSameSiteLax}, {Name: "persist", Value: "synthetic", Domain: ".moneyforward.com", Path: "/", Expires: cdp.TimeSinceEpoch(2000000000), Secure: true, SameSite: network.CookieSameSiteStrict}}
	s.Origins["https://moneyforward.com"] = map[string]string{"token": "synthetic-me"}
	s.Origins["https://id.moneyforward.com"] = map[string]string{"token": "synthetic-id"}
	return s
}
func TestRoundTripAndSeed(t *testing.T) {
	p := filepath.Join(t.TempDir(), "data", "state.json")
	s := fixture()
	if e := Save(p, s); e != nil {
		t.Fatal(e)
	}
	got, exists, e := Load(p, "")
	if e != nil || !exists || !reflect.DeepEqual(got, s) {
		t.Fatal(got, exists, e)
	}
	f, _ := os.Stat(p)
	d, _ := os.Stat(filepath.Dir(p))
	if f.Mode().Perm() != 0600 || d.Mode().Perm() != 0700 {
		t.Fatal("permissions")
	}
	seed := p
	destination := filepath.Join(t.TempDir(), "state.json")
	got, exists, e = Load(destination, seed)
	if e != nil || !exists || !reflect.DeepEqual(s, got) {
		t.Fatal("seed", e)
	}
	_ = os.WriteFile(destination, []byte("broken"), 0600)
	if _, _, e = Load(destination, seed); e == nil {
		t.Fatal("corrupt state fell back to seed")
	}
	_ = os.WriteFile(destination, []byte(`{"version":999}`), 0600)
	if _, _, e = Load(destination, ""); e == nil {
		t.Fatal("unknown version")
	}
	if _, _, e = Load(filepath.Join(t.TempDir(), "missing"), ""); e != nil {
		t.Fatal(e)
	}
	files, _ := filepath.Glob(filepath.Join(filepath.Dir(p), ".auth-state-*"))
	if len(files) > 0 {
		t.Fatal("temporary state leaked")
	}
}
func TestRejectUnexpectedDomains(t *testing.T) {
	for _, domain := range []string{"moneyforward.com.example.org", "evil.org"} {
		s := fixture()
		s.Cookies[0].Domain = domain
		if s.Validate() == nil {
			t.Fatal(domain)
		}
	}
	s := fixture()
	s.Origins["https://evil.org"] = map[string]string{}
	if s.Validate() == nil {
		t.Fatal("unexpected origin")
	}
}

func TestPrepareSecuresExistingOwnedDirectory(t *testing.T) {
	for _, mode := range []os.FileMode{0755, 0777} {
		dir := filepath.Join(t.TempDir(), "state")
		if e := os.Mkdir(dir, mode); e != nil {
			t.Fatal(e)
		}
		if e := os.Chmod(dir, mode); e != nil {
			t.Fatal(e)
		}
		if e := Prepare(filepath.Join(dir, "auth-state.json")); e != nil {
			t.Fatal(e)
		}
		info, e := os.Stat(dir)
		if e != nil || info.Mode().Perm() != 0700 {
			t.Fatalf("existing directory was not secured: %v %v", info, e)
		}
	}
}

func TestPreparePreservesWorkingDirectoryThroughAlias(t *testing.T) {
	dir := t.TempDir()
	if e := os.Chmod(dir, 0755); e != nil {
		t.Fatal(e)
	}
	t.Chdir(dir)
	alias := filepath.Join(t.TempDir(), "alias")
	if e := os.Symlink(dir, alias); e != nil {
		t.Fatal(e)
	}
	for _, path := range []string{"auth-state.json", filepath.Join(alias, "auth-state.json")} {
		if e := Prepare(path); e != nil {
			t.Fatal(e)
		}
		info, e := os.Stat(dir)
		if e != nil || info.Mode().Perm() != 0755 {
			t.Fatalf("working directory permissions changed: %v %v", info, e)
		}
	}
}
func TestLockAndProcessExit(t *testing.T) {
	if p := os.Getenv("MFR_TEST_LOCK_PATH"); p != "" {
		lock, e := Acquire(p)
		if e != nil {
			t.Fatal(e)
		}
		_ = lock
		return
	}
	p := filepath.Join(t.TempDir(), "state.json")
	l, e := Acquire(p)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = Acquire(p); failure.Code(e) != 6 {
		t.Fatal("lock conflict", e)
	}
	_ = l.Close()
	cmd := exec.Command(os.Args[0], "-test.run=^TestLockAndProcessExit$")
	cmd.Env = append(os.Environ(), "MFR_TEST_LOCK_PATH="+p)
	if data, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("child failed: %s %v", data, e)
	}
	l, e = Acquire(p)
	if e != nil {
		t.Fatal("lock retained after process exit", e)
	}
	_ = l.Close()
	b, _ := json.Marshal(fixture())
	_ = os.WriteFile(p, b, 0600)
}
