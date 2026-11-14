package model

import "strings"

// normalizeName trims the whitespace around name and rejects what is then
// empty. Every name in the catalog goes through it, when it is first given and
// when it is changed.
func normalizeName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", ErrNameEmpty
	}
	return name, nil
}
