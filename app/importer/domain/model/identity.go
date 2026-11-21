package model

import (
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

// VersionMark sits between the ID of a unit and its version in an idempotency key.
const VersionMark = "@"

// Key is the idempotency key of the unit's transaction, or of the first of them:
// "moze:", the ID of the unit, and its version.
//
// The version is in the key because the ledger answers an idempotency key it has
// seen with the transaction stored under it, whatever is asked this time. A key made
// of the rows alone would say that a row that was recorded is recorded, however the
// mapping, a row override or the rates have changed since; with the version, what
// the plan says now is another key than what it said when the draft was recorded,
// and the difference is seen: see Unit.ID and ParseKey.
func (u Unit) Key() string {
	return KeyPrefix + u.ID + VersionMark + u.Version
}

// Keys are the idempotency keys of the transactions the unit records.
func (u Unit) Keys() []string {
	if u.Kind == KindExpenseReward {
		return []string{u.Key(), u.Key() + RewardKeySuffix}
	}
	return []string{u.Key()}
}

// ParsedKey is what an idempotency key made by the import says.
type ParsedKey struct {
	// ID and Version are those of the unit that made it; Version is empty for a key
	// of an earlier form that had none.
	ID      string
	Version string
	// Reward is true for the key of the reward of a KindExpenseReward.
	Reward bool
}

// Rows are the keys of the rows the unit is made of, as far as its ID says: all of
// them for one row or a pair, the first for a plan.
func (p ParsedKey) Rows() []string {
	return strings.Split(strings.TrimPrefix(p.ID, "plan-"), "+")
}

// ParseKey reads an idempotency key as the import makes them. It returns false for a
// key that is not the import's, which does not start with KeyPrefix.
func ParseKey(key string) (ParsedKey, bool) {
	rest, ok := strings.CutPrefix(key, KeyPrefix)
	if !ok || rest == "" {
		return ParsedKey{}, false
	}
	var p ParsedKey
	rest, p.Reward = strings.CutSuffix(rest, RewardKeySuffix)
	if i := strings.LastIndex(rest, VersionMark); i >= 0 {
		p.ID, p.Version = rest[:i], rest[i+len(VersionMark):]
	} else {
		p.ID = rest
	}
	return p, p.ID != ""
}

// Fingerprint is a short hash of everything the unit says to the ledger besides what
// it is made of: what kind it is, its day and time, its text, its area, project and
// tags, the legs, lines, fees and periods with their amounts, and the party. Two
// plans of one unit have the same fingerprint exactly when the ledger would be asked
// to record the same thing; the order of the tags does not matter, as it does not to
// the ledger.
func (u Unit) Fingerprint() string {
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

	put("kind", string(u.Kind))
	put("day", u.Date.Format(time.DateOnly), u.Clock)
	put("text", u.Description, u.Merchant)
	put("where", u.Area, u.Project)
	tags := slices.Clone(u.Tags)
	slices.Sort(tags)
	put(append([]string{"tags"}, tags...)...)
	put("main", u.Main.Account, u.Main.Instrument, u.Main.Amount.String())
	if u.Other != nil {
		put("other", u.Other.Account, u.Other.Instrument, u.Other.Amount.String())
	}
	for _, l := range u.Lines {
		put("line", l.Category, l.Amount.String(), opt(l.Base), opt(l.Discount), l.Memo)
	}
	put("party", u.Direction, u.Party)
	put("base", opt(u.Base))
	if u.Reward != nil {
		put("reward", u.Reward.Amount.String(), opt(u.Reward.Base), u.Reward.Description)
	}
	for _, f := range u.Fees {
		put("fee", f.Account, f.Instrument, f.Category, f.Amount.String(), opt(f.Base), f.Memo)
	}
	for _, p := range u.Periods {
		put("period", p.DueOn.Format(time.DateOnly), p.Principal.String(), opt(p.Interest))
	}
	put("interest", u.InterestCategory)

	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:6])
}
