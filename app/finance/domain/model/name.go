// Package model holds the finance entities and the invariants they must keep:
// the reference data every transaction points at (instruments, institutions,
// accounts and categories) and the ledger, in which a transaction is a set of
// postings that add up to zero in each instrument.
package model

import "strings"

// normalizeName trims the whitespace around name and rejects what is then
// empty. Every name in the model goes through it.
func normalizeName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", ErrNameEmpty
	}
	return name, nil
}
