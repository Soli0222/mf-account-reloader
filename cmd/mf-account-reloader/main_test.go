package main

import (
	"errors"
	"github.com/Soli0222/mf-account-reloader/internal/state"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"
)

func TestSignalCleanup(t *testing.T) {
	if os.Getenv("MFR_SIGNAL_HELPER") == "true" {
		os.Args = []string{"mfr", "run", "--auth-state-file", os.Getenv("MFR_SIGNAL_STATE"), "--browser-path", os.Getenv("MFR_SIGNAL_BROWSER")}
		os.Exit(run())
	}
	for _, signal := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM} {
		t.Run(strconv.Itoa(int(signal)), func(t *testing.T) {
			dir := t.TempDir()
			p := filepath.Join(dir, "state", "auth-state.json")
			stub := filepath.Join(dir, "browser")
			if e := os.WriteFile(stub, []byte("#!/bin/sh\nexec sleep 30\n"), 0700); e != nil {
				t.Fatal(e)
			}
			cmd := exec.Command(os.Args[0], "-test.run=^TestSignalCleanup$")
			cmd.Env = append(os.Environ(), "MFR_SIGNAL_HELPER=true", "MFR_SIGNAL_STATE="+p, "MFR_SIGNAL_BROWSER="+stub)
			if e := cmd.Start(); e != nil {
				t.Fatal(e)
			}
			defer cmd.Process.Kill()
			deadline := time.Now().Add(5 * time.Second)
			for {
				if _, e := os.Stat(p + ".lock"); e == nil {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("lock not acquired")
				}
				time.Sleep(10 * time.Millisecond)
			}
			if e := cmd.Process.Signal(signal); e != nil {
				t.Fatal(e)
			}
			e := cmd.Wait()
			var exit *exec.ExitError
			if !errors.As(e, &exit) || exit.ExitCode() != 128+int(signal) {
				t.Fatalf("exit=%v want=%d", e, 128+int(signal))
			}
			lock, e := state.Acquire(p)
			if e != nil {
				t.Fatal("state lock survived signal", e)
			}
			_ = lock.Close()
		})
	}
}
