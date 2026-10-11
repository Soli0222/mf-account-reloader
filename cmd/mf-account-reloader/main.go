package main

import (
	"context"
	"github.com/Soli0222/mf-account-reloader/internal/cli"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
)

func main() { os.Exit(run()) }
func run() int {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(ch)
	var interrupted atomic.Int32
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case s := <-ch:
			if s == syscall.SIGINT {
				interrupted.Store(130)
			} else {
				interrupted.Store(143)
			}
			cancel()
		case <-done:
		}
	}()
	code := cli.Execute(ctx, os.Args[1:], os.Stdout, os.Stderr, os.Getenv)
	if v := interrupted.Load(); v != 0 {
		return int(v)
	}
	return code
}
