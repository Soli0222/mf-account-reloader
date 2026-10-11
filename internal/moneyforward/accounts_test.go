package moneyforward

import "testing"

func TestDomainAndPath(t *testing.T) {
	for _, u := range []string{"https://moneyforward.com.example.org/accounts", "https://id.moneyforward.com/accounts", "https://moneyforward.com/accounts/show/alpha", "https://evil@moneyforward.com/accounts", "http://moneyforward.com/accounts", "https://moneyforward.com:444/accounts"} {
		if SameAccountsURL(u, AccountsURL) {
			t.Fatal(u)
		}
	}
	if !SameAccountsURL(AccountsURL+"?safe=query", AccountsURL) {
		t.Fatal("accounts path")
	}
}
func TestSelector(t *testing.T) {
	a := []Account{{ID: "opaque-1", Name: " same ", Automatic: true, CanUpdate: true}, {ID: "opaque-2", Name: "same", Automatic: true, CanUpdate: true}, {ID: "opaque-3", Name: "manual"}}
	if _, e := Resolve(a, nil, []string{"same"}); e == nil {
		t.Fatal("ambiguous accepted")
	}
	if _, e := Resolve(a, nil, []string{"sa"}); e == nil {
		t.Fatal("partial match accepted")
	}
	if _, e := Resolve(a, []string{"opaque-3"}, nil); e == nil {
		t.Fatal("manual accepted")
	}
	s, e := Resolve(a, []string{"opaque-1", "opaque-1"}, nil)
	if e != nil || len(s) != 1 {
		t.Fatal(s, e)
	}
}

func TestObservedUpdatingRequiresUniqueIdentity(t *testing.T) {
	a := &Adapter{}
	s := Snapshot{Accounts: []Account{
		{ID: "unique", Status: "updating"},
		{ID: "duplicate", Status: "updating"},
		{ID: "duplicate", Status: "ok"},
		{ID: "ambiguous", Status: "updating", Ambiguous: true},
		{ID: "", Status: "updating"},
	}}
	a.observe(&s)
	for i := range s.Accounts {
		s.Accounts[i].Status = "ok"
		s.Accounts[i].ObservedUpdating = false
	}
	a.observe(&s)
	for i, x := range s.Accounts {
		if x.ObservedUpdating != (i == 0) {
			t.Fatalf("unexpected completion evidence for row %d: %+v", i, x)
		}
	}
}
