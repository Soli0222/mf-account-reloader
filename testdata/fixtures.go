// Package testdata embeds entirely synthetic fixtures into the integration binary.
package testdata

import _ "embed"

//go:embed accounts.html
var Accounts string
