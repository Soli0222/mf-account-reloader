package moneyforward

import (
	"github.com/Soli0222/mf-account-reloader/internal/failure"
	"strings"
)

type Account struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Status    string `json:"status"`
	CanUpdate bool   `json:"canUpdate"`
	Automatic bool   `json:"automatic"`
	Ambiguous bool   `json:"ambiguous"`
}
type Snapshot struct {
	Structure   bool      `json:"structure"`
	Login       bool      `json:"login"`
	BulkCount   int       `json:"bulkCount"`
	BulkEnabled bool      `json:"bulkEnabled"`
	Accounts    []Account `json:"accounts"`
	Receipt     string    `json:"receipt"`
}

func Resolve(a []Account, ids, names []string) ([]Account, error) {
	out := []Account{}
	seen := map[string]bool{}
	add := func(matches []Account) error {
		if len(matches) != 1 {
			return failure.New(2, "account selector is missing or ambiguous; use a unique account ID")
		}
		x := matches[0]
		if x.ID == "" || x.Ambiguous || !x.Automatic || (!x.CanUpdate && x.Status != "updating") {
			return failure.New(2, "selected account cannot be individually updated")
		}
		if !seen[x.ID] {
			out = append(out, x)
			seen[x.ID] = true
		}
		return nil
	}
	for _, id := range ids {
		var m []Account
		for _, x := range a {
			if x.ID == id {
				m = append(m, x)
			}
		}
		if e := add(m); e != nil {
			return nil, e
		}
	}
	for _, name := range names {
		var m []Account
		for _, x := range a {
			if strings.TrimSpace(x.Name) == strings.TrimSpace(name) {
				m = append(m, x)
			}
		}
		if e := add(m); e != nil {
			return nil, e
		}
	}
	return out, nil
}
