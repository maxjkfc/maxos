package model

import (
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

// FormatVersion says which wording and which shape the import writes for the inputs of
// a unit: the labels of its descriptions, the lines it adds to them, how it is made of
// its rows. It is part of every Version.
//
// Bump it deliberately when what the import writes for the same inputs changes (a
// word of a label, the order of the lines of a description, the way a unit is made of
// its rows): that is the one intended way to mark every unit that is recorded as
// changed. Do not bump it for anything that leaves what is written as it was.
const FormatVersion = 1

// Fingerprint is a short hash of the inputs of the unit: what it is made of, and what
// the mapping, the row overrides and the rates make of it, with FormatVersion. Two
// plans of one unit have the same fingerprint exactly when their inputs are the same,
// and so when the import writes the same thing for them.
//
// It has the kind and the ID; the keys of all the rows the unit is made of, each of
// which is a hash of everything its row says, so that every word and number of the rows
// is in the fingerprint, those of the rows of a plan that its ID does not name
// included; its day and time, since an opening balance has a day that no row has; its
// area, project and tags; its legs, lines, fees, periods and reward, with their
// amounts; its direction and party; its interest category; and its notes.
//
// It does not have the text of the unit: the description and the merchant, the memos
// of the lines and of the fees, and the description of the reward. They are made of the
// words of the rows, which the keys of the rows have, and of the labels that the plan
// writes around them, which FormatVersion stands for and the notes say where the
// mapping decides; so a new wording is a new version only when it is made one on
// purpose. Nor does it have the place of the unit in the order of the plan, and the
// order of the tags does not matter, as it does not to the ledger.
func (u Unit) Fingerprint() string { return u.fingerprint(FormatVersion) }

// fingerprint is Fingerprint with the format of the given version.
func (u Unit) fingerprint(format int) string {
	var b strings.Builder
	put := func(parts ...string) {
		for _, p := range parts {
			b.WriteString(p)
			b.WriteByte(0x1f)
		}
		b.WriteByte(0x1e)
	}
	opt := func(d *decimal.Decimal) string {
		if d == nil {
			return ""
		}
		return d.String()
	}

	put("format", strconv.Itoa(format))
	put("kind", string(u.Kind), u.ID)
	put(append([]string{"rows"}, u.Rows...)...)
	put("day", u.Date.Format(time.DateOnly), u.Clock)
	put("where", u.Area, u.Project)
	tags := slices.Clone(u.Tags)
	slices.Sort(tags)
	put(append([]string{"tags"}, tags...)...)
	for _, n := range u.Notes {
		put("note", string(n.Kind), n.Value)
	}
	put("main", u.Main.Account, u.Main.Instrument, u.Main.Amount.String())
	if u.Other != nil {
		put("other", u.Other.Account, u.Other.Instrument, u.Other.Amount.String())
	}
	for _, l := range u.Lines {
		put("line", l.Category, l.Amount.String(), opt(l.Base), opt(l.Discount))
	}
	put("party", u.Direction, u.Party)
	put("base", opt(u.Base))
	if u.Reward != nil {
		put("reward", u.Reward.Amount.String(), opt(u.Reward.Base))
	}
	for _, f := range u.Fees {
		put("fee", f.Account, f.Instrument, f.Category, f.Amount.String(), opt(f.Base))
	}
	for _, p := range u.Periods {
		put("period", p.DueOn.Format(time.DateOnly), p.Principal.String(), opt(p.Interest))
	}
	put("interest", u.InterestCategory)

	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:6])
}
