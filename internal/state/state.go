// Package state persists authentication only. The browser profile is disposable.
package state

import (
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/Soli0222/mf-account-reloader/internal/failure"
	"github.com/chromedp/cdproto/network"
	"golang.org/x/sys/unix"
)

const Version = 1

type State struct {
	Version int                          `json:"version"`
	Cookies []*network.CookieParam       `json:"cookies"`
	Origins map[string]map[string]string `json:"origins"`
}

func Empty() State {
	return State{Version: Version, Cookies: []*network.CookieParam{}, Origins: map[string]map[string]string{}}
}
func AllowedOrigin(s string) bool {
	return s == "https://moneyforward.com" || s == "https://id.moneyforward.com"
}
func AllowedDomain(s string) bool {
	s = strings.TrimPrefix(s, ".")
	return s == "moneyforward.com" || s == "id.moneyforward.com"
}
func (s State) Validate() error {
	if s.Version != Version {
		return failure.New(2, "unsupported authentication state version")
	}
	for _, c := range s.Cookies {
		if c == nil || !AllowedDomain(c.Domain) {
			return failure.New(2, "authentication state contains an unexpected cookie domain")
		}
		if c.URL != "" {
			u, e := url.Parse(c.URL)
			if e != nil || u.User != nil || !AllowedOrigin(u.Scheme+"://"+u.Host) {
				return failure.New(2, "unexpected cookie URL")
			}
		}
	}
	for origin := range s.Origins {
		if !AllowedOrigin(origin) {
			return failure.New(2, "authentication state contains an unexpected origin")
		}
	}
	return nil
}
func Load(path, seed string) (State, bool, error) {
	f, e := os.Open(path)
	if errors.Is(e, os.ErrNotExist) {
		if seed == "" {
			return Empty(), false, nil
		}
		f, e = os.Open(seed)
	}
	if e != nil {
		return State{}, false, failure.New(2, "cannot read authentication state")
	}
	defer f.Close()
	dec := json.NewDecoder(io.LimitReader(f, 8<<20))
	dec.DisallowUnknownFields()
	var s State
	if dec.Decode(&s) != nil {
		return s, false, failure.New(2, "invalid authentication state JSON")
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		return s, false, failure.New(2, "invalid authentication state trailing data")
	}
	if e = s.Validate(); e != nil {
		return s, false, e
	}
	return s, true, nil
}
func Prepare(path string) error {
	dir, e := filepath.Abs(filepath.Dir(path))
	if e != nil {
		return e
	}
	if e = os.MkdirAll(dir, 0700); e != nil {
		return failure.New(1, "cannot create state directory")
	}
	info, e := os.Stat(dir)
	if e != nil {
		return failure.New(1, "cannot inspect state directory")
	}
	// Shared mount roots may belong to root and be writable via Kubernetes fsGroup.
	// Never change the permissions of the caller's home, working directory or /tmp.
	working, _ := os.Getwd()
	home, _ := os.UserHomeDir()
	working, _ = filepath.EvalSymlinks(working)
	home, _ = filepath.EvalSymlinks(home)
	tmp, _ := filepath.EvalSymlinks(os.TempDir())
	canonical, _ := filepath.EvalSymlinks(dir)
	shared := canonical == working || canonical == home || canonical == tmp || canonical == "/"
	if !shared {
		if st, ok := info.Sys().(*syscall.Stat_t); ok && int(st.Uid) == os.Getuid() {
			if e = os.Chmod(dir, 0700); e != nil {
				return failure.New(1, "cannot secure state directory")
			}
		}
	}
	if e = unix.Access(dir, unix.W_OK|unix.X_OK); e != nil {
		return failure.New(1, "state directory is not writable")
	}
	return nil
}
func Save(path string, s State) error {
	if e := s.Validate(); e != nil {
		return e
	}
	if e := Prepare(path); e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".auth-state-*")
	if e != nil {
		return failure.New(1, "cannot create temporary state")
	}
	name := f.Name()
	defer os.Remove(name)
	defer f.Close()
	if e = f.Chmod(0600); e == nil {
		e = json.NewEncoder(f).Encode(s)
	}
	if e == nil {
		e = f.Sync()
	}
	if e == nil {
		e = f.Close()
	}
	if e == nil {
		e = os.Rename(name, path)
	}
	if e != nil {
		return failure.New(1, "cannot atomically save authentication state")
	}
	d, e := os.Open(filepath.Dir(path))
	if e != nil {
		return e
	}
	defer d.Close()
	return d.Sync()
}

type Lock struct{ file *os.File }

func Acquire(path string) (*Lock, error) {
	abs, e := filepath.Abs(path)
	if e != nil {
		return nil, e
	}
	// Resolve directory symlinks so aliases of one state share the same lock.
	if e = Prepare(abs); e != nil {
		return nil, e
	}
	dir, e := filepath.EvalSymlinks(filepath.Dir(abs))
	if e != nil {
		return nil, e
	}
	abs = filepath.Join(dir, filepath.Base(abs))
	if info, e := os.Lstat(abs); e == nil && info.Mode()&os.ModeSymlink != 0 {
		return nil, failure.New(2, "state file must not be a symlink")
	}
	fd, e := unix.Open(abs+".lock", unix.O_CREAT|unix.O_RDWR|unix.O_NOFOLLOW, 0600)
	if e != nil {
		return nil, failure.New(1, "cannot open state lock")
	}
	f := os.NewFile(uintptr(fd), abs+".lock")
	if e = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); e != nil {
		f.Close()
		if errors.Is(e, unix.EWOULDBLOCK) {
			return nil, failure.New(6, "authentication state is already in use")
		}
		return nil, failure.New(1, "cannot acquire state lock")
	}
	return &Lock{f}, nil
}
func (l *Lock) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	_ = unix.Flock(int(l.file.Fd()), unix.LOCK_UN)
	return l.file.Close()
}
