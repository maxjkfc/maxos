package model

import (
	"strconv"
	"strings"
)

// VersionMark sits between the ID of a unit and its version in an idempotency key.
const VersionMark = "@"

// AttemptMark sits between the version and the number of the attempt in the key of a
// unit that is recorded again: see KeyAt.
const AttemptMark = "~"

// PlanIDPrefix starts the ID of a unit that is an instalment plan; what follows is the
// key of the first row of the plan.
const PlanIDPrefix = "plan-"

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
	return u.KeyAt(1)
}

// KeyAt is the key of the unit's transaction at an attempt. The first attempt is Key.
// The ledger keeps the key of a transaction that was voided, and answers it with that
// transaction, so a version that was voided and is wanted again (the plan went back to
// what it said before) is recorded under a key of its own: the version and the number
// of the attempt, which ParseKey tells apart.
func (u Unit) KeyAt(attempt int) string {
	key := KeyPrefix + u.ID + VersionMark + u.Version
	if attempt > 1 {
		key += AttemptMark + strconv.Itoa(attempt)
	}
	return key
}

// Keys are the idempotency keys of the transactions the unit records, at its first
// attempt.
func (u Unit) Keys() []string {
	return u.KeysAt(1)
}

// KeysAt are the idempotency keys of the transactions the unit records at an attempt.
func (u Unit) KeysAt(attempt int) []string {
	key := u.KeyAt(attempt)
	if u.Kind == KindExpenseReward {
		return []string{key, key + RewardKeySuffix}
	}
	return []string{key}
}

// ParsedKey is what an idempotency key made by the import says.
type ParsedKey struct {
	// ID and Version are those of the unit that made it; Version is empty for a key
	// of an earlier form that had none.
	ID      string
	Version string
	// Attempt is the number of the attempt, from 1.
	Attempt int
	// Reward is true for the key of the reward of a KindExpenseReward.
	Reward bool
}

// IsPlan tells whether the key is that of an instalment plan.
func (p ParsedKey) IsPlan() bool { return strings.HasPrefix(p.ID, PlanIDPrefix) }

// Rows are the keys of the rows the unit is made of, as far as its ID says: all of
// them for one row or a pair, the first for a plan.
func (p ParsedKey) Rows() []string {
	return strings.Split(strings.TrimPrefix(p.ID, PlanIDPrefix), "+")
}

// ParseKey reads an idempotency key as the import makes them. It returns false for a
// key that is not the import's, which does not start with KeyPrefix.
func ParseKey(key string) (ParsedKey, bool) {
	rest, ok := strings.CutPrefix(key, KeyPrefix)
	if !ok || rest == "" {
		return ParsedKey{}, false
	}
	p := ParsedKey{Attempt: 1}
	rest, p.Reward = strings.CutSuffix(rest, RewardKeySuffix)
	i := strings.LastIndex(rest, VersionMark)
	if i < 0 {
		p.ID = rest
		return p, p.ID != ""
	}
	p.ID, p.Version = rest[:i], rest[i+len(VersionMark):]
	if j := strings.LastIndex(p.Version, AttemptMark); j >= 0 {
		if n, err := strconv.Atoi(p.Version[j+len(AttemptMark):]); err == nil && n > 1 {
			p.Version, p.Attempt = p.Version[:j], n
		}
	}
	return p, p.ID != ""
}
