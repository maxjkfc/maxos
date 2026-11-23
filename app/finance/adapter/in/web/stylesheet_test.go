package web

import (
	"regexp"
	"strings"
	"testing"
)

// ruleHas is whether the stylesheet has a rule for the selector, whatever else the rule selects, with the declaration:
// the comments are not part of it, and neither is the layout of the text.
func ruleHas(css, selector, property, value string) bool {
	css = regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAllString(css, "")
	for _, rule := range regexp.MustCompile(`([^{}]+)\{([^{}]*)\}`).FindAllStringSubmatch(css, -1) {
		for _, s := range strings.Split(rule[1], ",") {
			if strings.Join(strings.Fields(s), " ") != selector {
				continue
			}
			for _, declaration := range strings.Split(rule[2], ";") {
				if name, v, ok := strings.Cut(declaration, ":"); ok && strings.TrimSpace(name) == property && strings.TrimSpace(v) == value {
					return true
				}
			}
		}
	}
	return false
}

// A name that is free text, with no place in it where a line may break, is as wide as it is written: in a table that makes
// the column of the name as wide, and pushes the amounts out of the card. So the names that stand beside amounts may be broken
// anywhere when there is no other place, which is what overflow-wrap: anywhere says of the width a name needs at least (as
// break-word, which only breaks what does not fit, does not). The measure that this holds is the page itself at every width
// (the overview of a ledger with a name of sixty letters, in Chromium and WebKit), which the tests of a page cannot see.
func TestTheNamesBesideAmountsMayBeBrokenAnywhere(t *testing.T) {
	css := newFake().site(t).get("/app/static/app.css").Body

	for _, selector := range []string{".in-card .balances th", ".due-name", ".row-name"} {
		if !ruleHas(css, selector, "overflow-wrap", "anywhere") {
			t.Errorf("the stylesheet does not let %s be broken anywhere, so a name that has no place to break widens its card", selector)
		}
	}
}
