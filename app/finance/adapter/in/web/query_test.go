package web

import (
	"net/http"
	"strings"
	"testing"
)

// A query that does not read is refused, never read as if what cannot be read was not
// there: ?month=%zz is not a request for this month, and net/http's own Query would
// answer it as one.

func TestAQueryThatDoesNotReadIsRefusedAndNotIgnored(t *testing.T) {
	f := populated()
	s := f.site(t)
	statement := "/app/cards/" + card.String()
	tests := []struct {
		name, target, want string
	}{
		{"a bad escape in a value", "/app/transactions?month=%zz", "網址的參數讀不懂"},
		{"a bad escape in a name", "/app/transactions?%zz=1", "網址的參數讀不懂"},
		{"a bad escape after a good parameter", "/app/transactions?month=2026-11&status=%", "網址的參數讀不懂"},
		{"a semicolon where the ampersand should be", "/app/transactions?month=2026-11;page=2", "網址的參數讀不懂"},
		{"the month twice", "/app/transactions?month=2026-11&month=2026-10", "month 只能給一個值"},
		{"the account twice", "/app/transactions?account=" + cash.String() + "&account=" + card.String(), "account 只能給一個值"},
		{"the status twice", "/app/transactions?status=draft&status=void", "status 只能給一個值"},
		{"the kind twice", "/app/transactions?kind=expense&kind=income", "kind 只能給一個值"},
		{"the page twice", "/app/transactions?page=1&page=2", "page 只能給一個值"},
		{"the budgets, a bad escape", "/app/budgets?month=%zz", "網址的參數讀不懂"},
		{"the budgets, the month twice", "/app/budgets?month=2026-11&month=2026-10", "month 只能給一個值"},
		{"the spending, a bad escape", "/app/spending?month=%zz", "網址的參數讀不懂"},
		{"the spending, a semicolon", "/app/spending?month=2026-11;x=1", "網址的參數讀不懂"},
		{"the spending, the month twice", "/app/spending?month=2026-11&month=2026-12", "month 只能給一個值"},
		{"a statement, a bad escape", statement + "?closing=%zz", "網址的參數讀不懂"},
		{"a statement, the closing day twice", statement + "?closing=2026-11-05&closing=2026-10-05", "closing 只能給一個值"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clear(f.calls)
			res := s.get(tt.target)
			if res.Code != http.StatusBadRequest {
				t.Fatalf("GET %s = %d, want 400:\n%s", tt.target, res.Code, res.Body)
			}
			wantIn(t, res.Body, tt.want, "狀態碼 400")
			if n := f.total(); n != 0 {
				t.Errorf("%d calls of use cases for a request that was refused: %v", n, f.calls)
			}
		})
	}

	// What a page does not take is not its business, even when it is given twice, and an
	// escape that reads is read.
	for _, target := range []string{
		"/app/transactions?x=1&x=2",
		"/app/transactions?month=%32%30%32%36-11",
		"/app/transactions?month=2026-11&status=",
		"/app/budgets?month=2026-11&%e4%b8%80=1",
	} {
		if got := s.get(target).Code; got != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", target, got)
		}
	}
}

func TestAnErrorPageDoesNotRepeatWhatItWasGiven(t *testing.T) {
	s := populated().site(t)
	long := strings.Repeat("ab", 50_000) // a hundred thousand characters, which a request may carry
	for _, target := range []string{
		"/app/transactions?month=" + long,
		"/app/transactions?account=" + long,
		"/app/transactions?page=" + long,
		"/app/budgets?month=" + long,
		"/app/spending?month=" + long,
		"/app/cards/" + card.String() + "?closing=" + long,
		"/app/transactions?month=<script>alert(1)</script>",
	} {
		res := s.get(target)
		if res.Code != http.StatusBadRequest {
			t.Fatalf("GET %.60s... = %d, want 400", target, res.Code)
		}
		if len(res.Body) > 20_000 {
			t.Errorf("GET %.60s...: the page is %d bytes, which repeats what was given", target, len(res.Body))
		}
		if strings.Contains(res.Body, "abab") || strings.Contains(res.Body, "alert(1)") {
			t.Errorf("GET %.60s...: the page repeats the value of the parameter", target)
		}
	}
}
