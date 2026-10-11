//go:build integration

package integration

import "time"

// Browser startup, intercepted redirects and CDP round trips need scheduling
// headroom on shared CI runners. These are failure bounds, not fixed sleeps.
const (
	testNavigationTimeout = 5 * time.Second
	testActionTimeout     = 2 * time.Second
	testLoginTimeout      = 10 * time.Second
	testRunTimeout        = 15 * time.Second
)
