package model

import "testing"

func TestFormatVersionIsPartOfTheFingerprint(t *testing.T) {
	u := Unit{Kind: KindIncome, ID: "abc-1", Rows: []string{"abc-1"}}
	if got, want := u.Fingerprint(), u.fingerprint(FormatVersion); got != want {
		t.Errorf("Fingerprint = %q, want the fingerprint of the current format, %q", got, want)
	}
	if u.fingerprint(FormatVersion) == u.fingerprint(FormatVersion+1) {
		t.Error("another format has the same fingerprint: bumping FormatVersion would mark no unit as changed")
	}
}
