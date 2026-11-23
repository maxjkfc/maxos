package web

import (
	"errors"
	"net/http"
	"os"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
)

// The pages use the words of CONTEXT.md and none of the ones it says to avoid. The
// words are read from the file, each entry of which ends with a line such as
//
//	_Avoid_: 科目、支出帳戶、領域
//
// so that a word added there is checked from then on, and none is kept in a list
// here that can fall behind it. A word that one entry avoids is not avoided when it is
// a word the glossary itself uses for something else: the name of an entry (專案 is
// avoided for an area, and is the name of the entry of the project), or the name of a
// kind of transaction, which the entry of the kinds (種類) lists (轉帳 is avoided for a
// trade, and is the kind of a transfer). And a word is not found inside the name of an
// entry (記錄 is avoided, and 未記錄 is an entry).
//
// Every page of every test is checked as it is fetched (site.page), so that the pages
// of every fixture there is are covered, not a few that this file picks.

const glossaryPath = "../../../../../CONTEXT.md"

// glossary is what is read of CONTEXT.md.
type glossary struct {
	// terms are the names of its entries, the longest first.
	terms []string
	// avoided are the words it says to avoid.
	avoided []string
}

var (
	glossaryOnce sync.Once
	glossaryRead glossary
	glossaryErr  error
)

var (
	termLine  = regexp.MustCompile(`^\*\*([^*]+)\*\*`)
	avoidLine = regexp.MustCompile(`^_Avoid_[:：]\s*(.+)$`)
)

// kindsEntry is the name of the entry that lists the kinds of a transaction.
const kindsEntry = "種類"

// readGlossary reads CONTEXT.md once.
func readGlossary(t *testing.T) glossary {
	t.Helper()
	glossaryOnce.Do(func() {
		data, err := os.ReadFile(glossaryPath)
		if err != nil {
			glossaryErr = err
			return
		}
		var (
			terms, avoid []string
			kinds        string // the definition of the entry of the kinds
			inKinds      bool
		)
		for line := range strings.SplitSeq(string(data), "\n") {
			line = strings.TrimSpace(line)
			if inKinds && line != "" {
				kinds, inKinds = line, false
			}
			if m := termLine.FindStringSubmatch(line); m != nil {
				terms = append(terms, strings.TrimSpace(m[1]))
				inKinds = strings.TrimSpace(m[1]) == kindsEntry
			}
			if m := avoidLine.FindStringSubmatch(line); m != nil {
				for _, w := range strings.FieldsFunc(m[1], func(r rune) bool { return strings.ContainsRune("、；;，,", r) }) {
					if w = strings.TrimSpace(w); w != "" {
						avoid = append(avoid, w)
					}
				}
			}
		}
		if kinds == "" {
			glossaryErr = os.ErrNotExist // the entry of the kinds is not where it was: the exception below would be empty
			return
		}
		glossaryRead.avoided = slices.DeleteFunc(slices.Compact(slices.Sorted(slices.Values(avoid))), func(w string) bool {
			return slices.Contains(terms, w) || strings.Contains(kinds, w)
		})
		glossaryRead.terms = slices.SortedFunc(slices.Values(terms), func(a, b string) int { return len(b) - len(a) })
	})
	if glossaryErr != nil {
		t.Fatalf("read the glossary %s: %v", glossaryPath, glossaryErr)
	}
	return glossaryRead
}

// wantNoAvoidedWords fails for each word of the glossary's avoid lists that the page
// has, outside the names of the entries.
func wantNoAvoidedWords(t *testing.T, target, page string) {
	t.Helper()
	g := readGlossary(t)
	for _, term := range g.terms {
		page = strings.ReplaceAll(page, term, "")
	}
	for _, word := range g.avoided {
		if strings.Contains(page, word) {
			t.Errorf("%s uses %q, which CONTEXT.md says to avoid", target, word)
		}
	}
}

func TestTheAvoidedWordsAreReadFromTheGlossary(t *testing.T) {
	words := readGlossary(t).avoided

	// Words that CONTEXT.md avoids, and that the pages must not use.
	for _, want := range []string{"科目", "餘額調整", "歸帳日", "紅利回饋", "待處理", "未入帳", "往來餘額", "代付支出", "幣種", "聯絡人", "記錄", "分類", "分期餘額", "存款", "預估餘額"} {
		if !slices.Contains(words, want) {
			t.Errorf("%q is not among the avoided words %q", want, words)
		}
	}
	// The name of an entry, or of a kind of transaction, is not avoided, though another
	// entry avoids it.
	for _, word := range []string{"專案", "帳戶", "領域", "手續費", "折扣", "轉帳", "收入", "預覽餘額"} {
		if slices.Contains(words, word) {
			t.Errorf("%q is a word of the glossary and is among the avoided words", word)
		}
	}
	// The entry of the preview balance is read: its name is a term, which the pages may use, and its words to avoid are not.
	if !slices.Contains(readGlossary(t).terms, "預覽餘額") {
		t.Errorf("預覽餘額 is not among the terms %q", readGlossary(t).terms)
	}

	// A word that is inside the name of an entry is not found there: 未記錄 is an entry,
	// 記錄 is avoided.
	wantNoAvoidedWords(t, "the name of an entry", "<th>未記錄</th>")
}

func TestOnlyGlossaryWordsAreUsed(t *testing.T) {
	f := populated()
	s := f.site(t)
	for _, route := range s.pages.Routes() {
		switch route.Pattern {
		case "/app/static/app.css", "/app", "/app/":
			// The stylesheet, the redirect, and what is answered for a path that is no page,
			// which is read below.
			continue
		}
		// Every route has a page here, and site.page reads it against the glossary.
		s.page(pathOf(t, route.Pattern))
	}

	// What the server says when a page is not there, or is not asked right, is words too.
	for _, target := range []string{"/app/nowhere", "/app/transactions?month=nov", "/app/transactions/" + seqID(999).String()} {
		res := s.get(target)
		if res.Code < 400 {
			t.Fatalf("GET %s = %d, want an error page", target, res.Code)
		}
		wantNoAvoidedWords(t, target, res.Body)
	}

	// So is what it says when it is not the user's doing: a method that no page answers
	// to, and a use case that failed.
	post := s.do(s.access.Request(t, http.MethodPost, "/app/", "max@example.com"))
	if post.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST /app/ = %d, want 405", post.Code)
	}
	wantNoAvoidedWords(t, "POST /app/", post.Body)
	f.failures["GetPreviewBalances"] = errors.New("connection refused")
	failed := s.get("/app/")
	if failed.Code != http.StatusInternalServerError {
		t.Fatalf("GET /app/ with a failing use case = %d, want 500", failed.Code)
	}
	wantNoAvoidedWords(t, "GET /app/ with a failing use case", failed.Body)
}
