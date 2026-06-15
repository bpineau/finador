package web

import (
	"fmt"
	"net/url"
	"strings"
	"testing"

	"finador/internal/domain"
)

func TestAccountsListAndCreate(t *testing.T) {
	srv, f := testServer(t)

	// list shows the fixture account + the creation form, and the nav link.
	code, body := get(t, srv, "/accounts")
	if code != 200 {
		t.Fatalf("GET /accounts = %d\n%s", code, excerpt(body))
	}
	for _, want := range []string{"PEA Zephyr", "gains:17.2%", "<form", `name="tax-mode"`, `href="/accounts"`} {
		if !strings.Contains(body, want) {
			t.Errorf("/accounts: %q missing", want)
		}
	}

	// create a CTO with a gains tax and two aliases.
	code, body, loc := postForm(t, srv, "/accounts", url.Values{
		"name": {"CTO Meridia"}, "ccy": {"USD"},
		"tax-mode": {"gains"}, "tax-rate": {"30"}, "aliases": {"cto, meridia"},
	})
	if code != 303 || !strings.HasPrefix(loc, "/accounts") {
		t.Fatalf("POST /accounts = %d → %q\n%s", code, loc, excerpt(body))
	}
	acc, err := f.Book.Account("cto") // resolves by alias
	if err != nil {
		t.Fatalf("created account not found by alias: %v", err)
	}
	if acc.Name != "CTO Meridia" || acc.Currency != "USD" || acc.Tax.String() != "gains:30%" {
		t.Errorf("created account = %+v, tax %s", acc, acc.Tax)
	}
	if len(acc.Aliases) != 2 || acc.Aliases[0] != "cto" || acc.Aliases[1] != "meridia" {
		t.Errorf("aliases = %v, want [cto meridia]", acc.Aliases)
	}

	// empty name → 400, nothing added.
	before := len(f.Book.Accounts)
	code, body, _ = postForm(t, srv, "/accounts", url.Values{"name": {""}, "ccy": {"EUR"}})
	if code != 400 || !strings.Contains(body, "name is required") {
		t.Fatalf("empty name = %d\n%s", code, excerpt(body))
	}
	if len(f.Book.Accounts) != before {
		t.Error("invalid create added an account")
	}

	// duplicate name → 400.
	code, _, _ = postForm(t, srv, "/accounts", url.Values{"name": {"PEA Zephyr"}, "ccy": {"EUR"}})
	if code != 400 {
		t.Errorf("duplicate name = %d, want 400", code)
	}
}

func TestAccountEdit(t *testing.T) {
	srv, f := testServer(t)

	// edit page pre-fills name, the selected tax mode and the rate as a percentage.
	code, body := get(t, srv, "/accounts/pea/edit")
	if code != 200 {
		t.Fatalf("GET edit = %d\n%s", code, excerpt(body))
	}
	for _, want := range []string{`value="PEA Zephyr"`, `value="gains" selected`, `value="17.2"`} {
		if !strings.Contains(body, want) {
			t.Errorf("edit form: %q missing\n%s", want, excerpt(body))
		}
	}

	// change tax to value:10%, currency to USD, add an alias.
	code, body, loc := postForm(t, srv, "/accounts/pea/edit", url.Values{
		"name": {"PEA Zephyr"}, "ccy": {"USD"},
		"tax-mode": {"value"}, "tax-rate": {"10"}, "aliases": {"pea, bourso"},
	})
	if code != 303 || !strings.HasPrefix(loc, "/accounts") {
		t.Fatalf("POST edit = %d → %q\n%s", code, loc, excerpt(body))
	}
	acc, _ := f.Book.Account("pea")
	if acc.Currency != "USD" || acc.Tax.String() != "value:10%" {
		t.Errorf("after edit: ccy %s tax %s", acc.Currency, acc.Tax)
	}
	if _, err := f.Book.Account("bourso"); err != nil {
		t.Errorf("added alias not resolvable: %v", err)
	}

	// unknown account → 404.
	if code, _ := get(t, srv, "/accounts/nope/edit"); code != 404 {
		t.Errorf("GET edit unknown = %d", code)
	}
}

func TestAccountDeleteGuard(t *testing.T) {
	srv, f := testServer(t)

	// pea has transactions → delete refused with a clear message, account kept.
	code, body, _ := postForm(t, srv, "/accounts/pea/delete", url.Values{})
	if code != 400 || !strings.Contains(body, "delete its transactions first") {
		t.Fatalf("guarded delete = %d\n%s", code, excerpt(body))
	}
	if _, err := f.Book.Account("pea"); err != nil {
		t.Error("guarded account was removed")
	}

	// a fresh account with no transactions deletes cleanly.
	id := domain.NewID()
	if err := f.Book.AddAccount(&domain.Account{ID: domain.AccountID(id), Name: "Livret A", Currency: domain.EUR}); err != nil {
		t.Fatal(err)
	}
	code, _, loc := postForm(t, srv, fmt.Sprintf("/accounts/%s/delete", id), url.Values{})
	if code != 303 || !strings.HasPrefix(loc, "/accounts") {
		t.Fatalf("clean delete = %d → %q", code, loc)
	}
	if _, err := f.Book.Account(id); err == nil {
		t.Error("account not removed")
	}
}
