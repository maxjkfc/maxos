package file

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/shopspring/decimal"
	"go.yaml.in/yaml/v3"

	"github.com/maxjkfc/maxos/app/importer/domain/model"
	"github.com/maxjkfc/maxos/app/importer/usecase/port/out/filefailure"
	"github.com/maxjkfc/maxos/app/importer/usecase/port/out/mapping"
)

var _ mapping.Store = (*MappingYAML)(nil)

// MappingYAML keeps the mapping in a YAML file that the user reads and edits.
//
// The file has these sections, each of them optional, with these keys; a key that
// is not one of them is a failure, and so is a key that is there twice:
//
//	instruments:  # by currency code: name, kind, is_currency, decimals, rate_as
//	accounts:     # by MOZE account name: name, kind, institution, closing_day, payment_due_day,
//	              # opening_date (a day, 2026-04-15)
//	projects:     # by value of 專案: kind, name, default_area, party; active_period is
//	              # ignored, see below
//	categories:   # expense and income, each by "主類別/子類別": a category path, or
//	              # {category, treatment, party, tag}
//	parties:      # MOZE name: ledger name
//	tags:         # MOZE tag without its "#": ledger tag name
//	defaults:     # fee_category, interest_category, refund_category
//	rows:         # by row key: treatment, category, party, tag, quantity
//
// The names of what is mapped are text whatever they look like: a key 2025 is the
// text "2025". The file says what the entries are, not whether that is right; the
// mapping is validated by model.Mapping.Validate.
//
// A project may have an active_period, which the first version of the import wrote. It
// is read and ignored, whatever it is written as, and Load has a warning for each, with
// the line of its key. Neither Load nor Append changes the file for it.
//
// What is wrong with a file is a *filefailure.Failure that has the file, the line and the
// values of its code, never a sentence: the words are the command line's.
//
// An entry that the import added ends with the comment "# guessed" until the user
// has looked at it and taken the comment away.
type MappingYAML struct{}

// NewMappingYAML returns a store of mapping files.
func NewMappingYAML() *MappingYAML { return &MappingYAML{} }

// guessedMark is the comment that ends the line of an entry the import has guessed.
const guessedMark = "# guessed"

// mappingDayExample is a day written the way the mapping has it: a failure for a text
// that is not a day shows it, so that the user sees what is meant.
const mappingDayExample = "2026-04-15"

// The sections of the file, and the keys of what they hold. The sections that are the
// model's, and the keys of its defaults, have the names the model gives them: they
// are how an entry of the mapping is told from the others.
const (
	secInstruments = model.SectionInstruments
	secAccounts    = model.SectionAccounts
	secProjects    = model.SectionProjects
	secCategories  = "categories"
	secParties     = model.SectionParties
	secTags        = model.SectionTags
	secDefaults    = model.SectionDefaults
	secRows        = model.SectionRows

	// The two sections of categories.
	secExpense = "expense"
	secIncome  = "income"

	keyName             = "name"
	keyKind             = "kind"
	keyIsCurrency       = "is_currency"
	keyDecimals         = "decimals"
	keyRateAs           = "rate_as"
	keyInstitution      = "institution"
	keyClosingDay       = "closing_day"
	keyPaymentDueDay    = "payment_due_day"
	keyOpeningDate      = "opening_date"
	keyDefaultArea      = "default_area"
	keyParty            = "party"
	keyActivePeriod     = "active_period"
	keyCategory         = "category"
	keyTreatment        = "treatment"
	keyTag              = "tag"
	keyQuantity         = "quantity"
	keyFeeCategory      = model.DefaultFeeCategory
	keyInterestCategory = model.DefaultInterestCategory
	keyRefundCategory   = model.DefaultRefundCategory
)

// sectionOrder is the order the sections are written in.
var sectionOrder = []string{
	secInstruments, secAccounts, secProjects, secCategories, secParties, secTags, secDefaults, secRows,
}

// categorySections are the sections of categories, in the order they are written in.
var categorySections = []string{secExpense, secIncome}

// The keys that each kind of entry may have.
var (
	instrumentKeys = []string{keyName, keyKind, keyIsCurrency, keyDecimals, keyRateAs}
	accountKeys    = []string{keyName, keyKind, keyInstitution, keyClosingDay, keyPaymentDueDay, keyOpeningDate}
	projectKeys    = []string{keyKind, keyName, keyDefaultArea, keyParty, keyActivePeriod}
	categoryKeys   = []string{keyCategory, keyTreatment, keyParty, keyTag}
	defaultsKeys   = []string{keyFeeCategory, keyInterestCategory, keyRefundCategory}
	rowKeys        = []string{keyTreatment, keyCategory, keyParty, keyTag, keyQuantity}
)

// Load reads the mapping at path. A file that does not exist is an empty mapping
// that does not exist; one that has nothing in it is an empty mapping that does.
// Guessed is the number of entries that end with the mark "# guessed": see
// countGuessed. Warnings has what is odd in the file and stops nothing. Load writes
// nothing. What is wrong with the file is a *filefailure.Failure that names it.
func (*MappingYAML) Load(ctx context.Context, path string) (mapping.Loaded, error) {
	if err := ctx.Err(); err != nil {
		return mapping.Loaded{}, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return mapping.Loaded{}, nil
	}
	if err != nil {
		return mapping.Loaded{}, unreadable(path, err)
	}
	doc, err := parseDocument(data)
	if err != nil {
		return mapping.Loaded{}, inFile(path, err)
	}
	return mapping.Loaded{Mapping: doc.content, Exists: true, Guessed: doc.guessed, Warnings: doc.warnings}, nil
}

// countGuessed counts the entries of the sections that end with the mark of a guess,
// "# guessed": the line comment after the value of the entry, or after its key when
// the entry goes on below it. What is not an entry's own comment is not the mark: a
// comment of a line of its own, the comment of a section or of the start of the file,
// one inside an entry, and the text of a value, which is not a comment. A comment is
// the mark when it begins with "# guessed" and the word ends there, so that
// "# guessed: not sure" is one and "# guessedly" and "#guessed" are not.
func countGuessed(root *yaml.Node) int {
	n := 0
	for _, section := range pairsOf(root) {
		if section.key.Value != secCategories {
			n += markedIn(pairsOf(section.val))
			continue
		}
		for _, sub := range pairsOf(section.val) {
			n += markedIn(pairsOf(sub.val))
		}
	}
	return n
}

// pair is a key of a mapping node and its value.
type pair struct{ key, val *yaml.Node }

// pairsOf lists the keys of a mapping node, and their values, in the order of the file;
// it is none for a node that is not a mapping.
func pairsOf(n *yaml.Node) []pair {
	n = resolved(n)
	if n.Kind != yaml.MappingNode {
		return nil
	}
	pairs := make([]pair, 0, len(n.Content)/2)
	for i := 0; i+1 < len(n.Content); i += 2 {
		pairs = append(pairs, pair{key: resolved(n.Content[i]), val: resolved(n.Content[i+1])})
	}
	return pairs
}

// markedIn counts the entries among the pairs that end with the mark. The comment of
// an entry is on its value, or on its key when the value is below or is not there.
func markedIn(entries []pair) int {
	n := 0
	for _, e := range entries {
		if isMark(e.key.LineComment) || isMark(e.val.LineComment) {
			n++
		}
	}
	return n
}

// isMark tells whether a line comment is the mark of a guess.
func isMark(comment string) bool {
	rest, ok := strings.CutPrefix(comment, guessedMark)
	if !ok {
		return false
	}
	next, _ := utf8.DecodeRuneInString(rest)
	return rest == "" || (!unicode.IsLetter(next) && !unicode.IsDigit(next) && next != '_')
}

// document is a mapping file as it was parsed: what it says, and the nodes of its
// text, which is what Append needs to add to it where the additions belong.
type document struct {
	// root is the mapping at the top of the file, nil for a file with no content.
	root    *yaml.Node
	content model.Mapping
	// guessed is how many entries of it end with the mark of a guess.
	guessed int
	// warnings are what is odd in it and stops nothing.
	warnings []model.Warning
}

// parseDocument reads a mapping file. A file with nothing in it but comments and
// blank lines is an empty document.
func parseDocument(data []byte) (document, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	var doc yaml.Node
	if err := dec.Decode(&doc); err != nil {
		if errors.Is(err, io.EOF) {
			return document{}, nil
		}
		return document{}, yamlFailure(err)
	}
	// Whatever is in a second document would be left out without a word.
	var next yaml.Node
	switch err := dec.Decode(&next); {
	case errors.Is(err, io.EOF):
	case err != nil:
		return document{}, yamlFailure(err)
	default:
		line := next.Line
		if len(next.Content) > 0 {
			line = next.Content[0].Line
		}
		return document{}, failAt(filefailure.SecondDocument, line)
	}

	if len(doc.Content) == 0 {
		return document{}, nil
	}
	root := resolved(doc.Content[0])
	if isNull(root) {
		return document{}, nil
	}
	if root.Kind != yaml.MappingNode {
		return document{}, failAt(filefailure.NotSections, root.Line)
	}
	d := &decoder{}
	content, err := d.mapping(root)
	if err != nil {
		return document{}, err
	}
	return document{root: root, content: content, warnings: d.warnings, guessed: countGuessed(root)}, nil
}

// yamlLine finds the line in the message of an error of the YAML library, which begins
// "yaml: line 3: " when it has one.
var yamlLine = regexp.MustCompile(`^yaml: line (\d+):`)

// yamlFailure is the failure that an error of the YAML library comes to: the line it
// names, when it names one, and none of its words, which are not the user's.
func yamlFailure(err error) error {
	line := 0
	if m := yamlLine.FindStringSubmatch(err.Error()); m != nil {
		if n, err := strconv.Atoi(m[1]); err == nil {
			line = n
		}
	}
	return failAt(filefailure.YAMLSyntax, line)
}

// decoder reads a mapping from the nodes of a file, walking them and not decoding into
// types, so that every key is known, every error has its line and the text of a name
// is the text that is written. What is odd in the file and stops nothing is kept as
// warnings, in the order of the file.
type decoder struct{ warnings []model.Warning }

// mapping reads the mapping from the node at the top of the file.
func (d *decoder) mapping(root *yaml.Node) (model.Mapping, error) {
	var m model.Mapping
	sections, err := objectOf(root, "", sectionOrder)
	if err != nil {
		return model.Mapping{}, err
	}
	for _, s := range sections {
		switch s.key {
		case secInstruments:
			m.Instruments, err = decodeSection(s, s.key, decodeInstrument)
		case secAccounts:
			m.Accounts, err = decodeSection(s, s.key, decodeAccount)
		case secProjects:
			m.Projects, err = decodeSection(s, s.key, d.project)
		case secCategories:
			m.Expense, m.Income, err = decodeCategories(s)
		case secParties:
			m.Parties, err = decodeSection(s, s.key, decodeName)
		case secTags:
			m.Tags, err = decodeSection(s, s.key, decodeName)
		case secDefaults:
			m.Defaults, err = decodeDefaults(s)
		case secRows:
			m.Rows, err = decodeSection(s, s.key, decodeRow)
		}
		if err != nil {
			return model.Mapping{}, err
		}
	}
	return m, nil
}

// decodeSection reads a section, which is a mapping from a name to its entry, with
// decode, which makes the entry of the node of its value and is given where it is. A
// section that has no entry is nil. The where of a section is its name in the file,
// "accounts" or "categories.expense", and that of an entry, "accounts.Name": how the
// subject of a problem is written.
func decodeSection[V any](section field, where string, decode func(f field, where string) (V, error)) (map[string]V, error) {
	entries, err := entriesOf(section.val, where)
	if err != nil || len(entries) == 0 {
		return nil, err
	}
	out := make(map[string]V, len(entries))
	for _, e := range entries {
		v, err := decode(e, where+"."+e.key)
		if err != nil {
			return nil, err
		}
		out[e.key] = v
	}
	return out, nil
}

// decodeName reads a party or a tag: the name of the ledger.
func decodeName(f field, _ string) (string, error) { return f.text() }

func decodeInstrument(f field, where string) (model.InstrumentMap, error) {
	var in model.InstrumentMap
	fields, err := objectOf(f.val, where, instrumentKeys)
	if err != nil {
		return in, err
	}
	for _, p := range fields {
		switch p.key {
		case keyName:
			in.Name, err = p.text()
		case keyKind:
			in.Kind, err = p.text()
		case keyIsCurrency:
			in.IsCurrency, err = p.boolean()
		case keyDecimals:
			in.Decimals, err = p.integer()
		case keyRateAs:
			in.RateAs, err = p.text()
		}
		if err != nil {
			return in, err
		}
	}
	return in, nil
}

func decodeAccount(f field, where string) (model.AccountMap, error) {
	var a model.AccountMap
	fields, err := objectOf(f.val, where, accountKeys)
	if err != nil {
		return a, err
	}
	for _, p := range fields {
		switch p.key {
		case keyName:
			a.Name, err = p.text()
		case keyKind:
			a.Kind, err = p.text()
		case keyInstitution:
			a.Institution, err = p.text()
		case keyClosingDay:
			a.ClosingDay, err = p.integer()
		case keyPaymentDueDay:
			a.PaymentDueDay, err = p.integer()
		case keyOpeningDate:
			a.OpeningDate, err = p.day()
		}
		if err != nil {
			return a, err
		}
	}
	return a, nil
}

// project reads a project. An active_period is read and ignored: the import never sets
// the active period of a project, but a mapping that an earlier version of it wrote
// has some, and is not refused for it. The key stays in the file; the warning says
// where it is.
func (d *decoder) project(f field, where string) (model.ProjectMap, error) {
	var pm model.ProjectMap
	fields, err := objectOf(f.val, where, projectKeys)
	if err != nil {
		return pm, err
	}
	for _, p := range fields {
		switch p.key {
		case keyKind:
			pm.Kind, err = p.text()
		case keyName:
			pm.Name, err = p.text()
		case keyDefaultArea:
			pm.DefaultArea, err = p.text()
		case keyParty:
			pm.Party, err = p.text()
		case keyActivePeriod:
			d.warnings = append(d.warnings, model.Warning{
				Code:    model.WarningActivePeriodIgnored,
				Subject: where,
				Args:    []string{strconv.Itoa(p.at.Line)},
			})
		}
		if err != nil {
			return pm, err
		}
	}
	return pm, nil
}

// decodeCategories reads the categories section: the expense and the income
// categories, each by the name MOZE gives it.
func decodeCategories(f field) (expense, income map[string]model.CategoryMap, err error) {
	sections, err := objectOf(f.val, f.key, categorySections)
	if err != nil {
		return nil, nil, err
	}
	for _, s := range sections {
		switch s.key {
		case secExpense:
			expense, err = decodeSection(s, model.SectionExpense, decodeCategory)
		case secIncome:
			income, err = decodeSection(s, model.SectionIncome, decodeCategory)
		}
		if err != nil {
			return nil, nil, err
		}
	}
	return expense, income, nil
}

// decodeCategory reads a category entry: the path of a ledger category alone, or the
// keys of what is made of the rows.
func decodeCategory(f field, where string) (model.CategoryMap, error) {
	var c model.CategoryMap
	if f.val.Kind == yaml.ScalarNode {
		var err error
		c.Category, err = f.text()
		return c, err
	}
	fields, err := objectOf(f.val, where, categoryKeys)
	if err != nil {
		return c, err
	}
	for _, p := range fields {
		switch p.key {
		case keyCategory:
			c.Category, err = p.text()
		case keyTreatment:
			c.Treatment, err = p.text()
		case keyParty:
			c.Party, err = p.text()
		case keyTag:
			c.Tag, err = p.text()
		}
		if err != nil {
			return c, err
		}
	}
	return c, nil
}

func decodeDefaults(f field) (model.Defaults, error) {
	var d model.Defaults
	fields, err := objectOf(f.val, f.key, defaultsKeys)
	if err != nil {
		return d, err
	}
	for _, p := range fields {
		switch p.key {
		case keyFeeCategory:
			d.FeeCategory, err = p.text()
		case keyInterestCategory:
			d.InterestCategory, err = p.text()
		case keyRefundCategory:
			d.RefundCategory, err = p.text()
		}
		if err != nil {
			return d, err
		}
	}
	return d, nil
}

func decodeRow(f field, where string) (model.RowOverride, error) {
	var o model.RowOverride
	fields, err := objectOf(f.val, where, rowKeys)
	if err != nil {
		return o, err
	}
	for _, p := range fields {
		switch p.key {
		case keyTreatment:
			o.Treatment, err = p.text()
		case keyCategory:
			o.Category, err = p.text()
		case keyParty:
			o.Party, err = p.text()
		case keyTag:
			o.Tag, err = p.text()
		case keyQuantity:
			o.Quantity, err = p.quantity()
		}
		if err != nil {
			return o, err
		}
	}
	return o, nil
}

// field is an entry of a YAML mapping: its key, and the nodes of the key and of the
// value, which say where in the file it is.
type field struct {
	key string
	at  *yaml.Node
	val *yaml.Node
}

// resolved follows an alias to the node it stands for.
func resolved(n *yaml.Node) *yaml.Node {
	for n.Kind == yaml.AliasNode {
		n = n.Alias
	}
	return n
}

// isNull tells whether the node is no value: nothing at all, or ~ or null.
func isNull(n *yaml.Node) bool { return n.Kind == yaml.ScalarNode && n.Tag == "!!null" }

// entriesOf lists the entries of a mapping node, in the order of the file, with any
// key; where is the name of the node, which is how a failure says what it is about. No
// value is a mapping with no entries.
func entriesOf(n *yaml.Node, where string) ([]field, error) {
	n = resolved(n)
	if isNull(n) {
		return nil, nil
	}
	if n.Kind != yaml.MappingNode {
		return nil, failAt(filefailure.MustBeMapping, n.Line, where)
	}
	fields := make([]field, 0, len(n.Content)/2)
	lines := make(map[string]int, len(n.Content)/2) // the line each key is on
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := resolved(n.Content[i]), resolved(n.Content[i+1])
		if k.Kind != yaml.ScalarNode {
			return nil, failAt(filefailure.KeyNotSingle, k.Line, where)
		}
		if first, twice := lines[k.Value]; twice {
			return nil, failAt(filefailure.KeyTwice, k.Line, where, k.Value, strconv.Itoa(first))
		}
		lines[k.Value] = k.Line
		fields = append(fields, field{key: k.Value, at: k, val: v})
	}
	return fields, nil
}

// objectOf lists the entries of a mapping node whose keys are only those of known; a
// key that is not one is a failure that lists the keys that are.
func objectOf(n *yaml.Node, where string, known []string) ([]field, error) {
	fields, err := entriesOf(n, where)
	if err != nil {
		return nil, err
	}
	for _, f := range fields {
		if !slices.Contains(known, f.key) {
			return nil, failAt(filefailure.KeyUnknown, f.at.Line, append([]string{f.key, where}, known...)...)
		}
	}
	return fields, nil
}

// text is the value as the text it is written with, whatever it looks like: an
// unquoted 2025 is "2025". No value is the empty text.
func (f field) text() (string, error) {
	if isNull(f.val) {
		return "", nil
	}
	if f.val.Kind != yaml.ScalarNode {
		return "", failAt(filefailure.ValueNotSingle, f.val.Line, f.key)
	}
	return f.val.Value, nil
}

// integer is the value as a whole number; no value is zero.
func (f field) integer() (int, error) {
	text, err := f.text()
	if err != nil || text == "" {
		return 0, err
	}
	n, err := strconv.Atoi(text)
	if err != nil {
		return 0, failAt(filefailure.NotWholeNumber, f.val.Line, f.key, text)
	}
	return n, nil
}

// boolean is the value as true or false; no value is false.
func (f field) boolean() (bool, error) {
	text, err := f.text()
	if err != nil {
		return false, err
	}
	switch strings.ToLower(text) {
	case "", "false":
		return false, nil
	case "true":
		return true, nil
	}
	return false, failAt(filefailure.NotBoolean, f.val.Line, f.key, text)
}

// day is the value as a day at midnight UTC, written 2006-01-02; no value is the
// zero time.
func (f field) day() (time.Time, error) {
	text, err := f.text()
	if err != nil || text == "" {
		return time.Time{}, err
	}
	day, err := time.Parse(time.DateOnly, text)
	if err != nil {
		return time.Time{}, failAt(filefailure.NotDay, f.val.Line, f.key, text, mappingDayExample)
	}
	return day, nil
}

// quantity is the value as a decimal, which may be written as a number or as text;
// no value is none.
func (f field) quantity() (*decimal.Decimal, error) {
	text, err := f.text()
	if err != nil || text == "" {
		return nil, err
	}
	d, err := decimal.NewFromString(text)
	if err != nil {
		return nil, failAt(filefailure.NotNumber, f.val.Line, f.key, text)
	}
	return &d, nil
}
