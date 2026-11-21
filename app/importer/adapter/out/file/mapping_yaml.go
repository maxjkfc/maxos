package file

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"
	"go.yaml.in/yaml/v3"

	"github.com/maxjkfc/maxos/app/importer/domain/model"
	"github.com/maxjkfc/maxos/app/importer/usecase/port/out/mapping"
)

var _ mapping.Store = (*MappingYAML)(nil)

// MappingYAML keeps the mapping in a YAML file that the user reads and edits.
//
// The file has these sections, each of them optional, with these keys; a key that
// is not one of them is an error, and so is a key that is there twice:
//
//	instruments:  # by currency code: name, kind, is_currency, decimals, rate_as
//	accounts:     # by MOZE account name: name, kind, institution, closing_day, payment_due_day,
//	              # opening_date (a day, 2026-04-15)
//	projects:     # by value of 專案: kind, name, default_area, party
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
// An entry that the import added ends with the comment "# guessed" until the user
// has looked at it and taken the comment away.
type MappingYAML struct{}

// NewMappingYAML returns a store of mapping files.
func NewMappingYAML() *MappingYAML { return &MappingYAML{} }

// guessedMark is the comment that ends the line of an entry the import has guessed.
const guessedMark = "# guessed"

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
	projectKeys    = []string{keyKind, keyName, keyDefaultArea, keyParty}
	categoryKeys   = []string{keyCategory, keyTreatment, keyParty, keyTag}
	defaultsKeys   = []string{keyFeeCategory, keyInterestCategory, keyRefundCategory}
	rowKeys        = []string{keyTreatment, keyCategory, keyParty, keyTag, keyQuantity}
)

// Load reads the mapping at path. A file that does not exist is an empty mapping
// that does not exist; one that has nothing in it is an empty mapping that does.
// Guessed is the number of lines of the file that say "# guessed".
func (*MappingYAML) Load(ctx context.Context, path string) (mapping.Loaded, error) {
	if err := ctx.Err(); err != nil {
		return mapping.Loaded{}, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return mapping.Loaded{}, nil
	}
	if err != nil {
		return mapping.Loaded{}, err
	}
	doc, err := parseDocument(data)
	if err != nil {
		return mapping.Loaded{}, fmt.Errorf("%s: %w", path, err)
	}
	return mapping.Loaded{Mapping: doc.content, Exists: true, Guessed: countGuessed(data)}, nil
}

// countGuessed counts the lines of the file that are marked as guessed.
func countGuessed(data []byte) int {
	n := 0
	for line := range bytes.Lines(data) {
		if bytes.Contains(line, []byte(guessedMark)) {
			n++
		}
	}
	return n
}

// document is a mapping file as it was parsed: what it says, and the nodes of its
// text, which is what Append needs to add to it where the additions belong.
type document struct {
	// root is the mapping at the top of the file, nil for a file with no content.
	root    *yaml.Node
	content model.Mapping
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
		return document{}, yamlError(err)
	}
	// Whatever is in a second document would be left out without a word.
	var next yaml.Node
	switch err := dec.Decode(&next); {
	case errors.Is(err, io.EOF):
	case err != nil:
		return document{}, yamlError(err)
	default:
		line := next.Line
		if len(next.Content) > 0 {
			line = next.Content[0].Line
		}
		return document{}, fmt.Errorf("line %d: the file has a second YAML document; it may have only one", line)
	}

	if len(doc.Content) == 0 {
		return document{}, nil
	}
	root := resolved(doc.Content[0])
	if isNull(root) {
		return document{}, nil
	}
	if root.Kind != yaml.MappingNode {
		return document{}, fmt.Errorf("line %d: the file must be a mapping of sections", root.Line)
	}
	content, err := decodeMapping(root)
	if err != nil {
		return document{}, err
	}
	return document{root: root, content: content}, nil
}

// yamlError makes an error of the YAML library read like the others of the adapter.
func yamlError(err error) error {
	return errors.New(strings.TrimPrefix(err.Error(), "yaml: "))
}

// decodeMapping reads the mapping from the node at the top of the file, walking the
// nodes and not decoding into types, so that every key is known, every error has
// its line and the text of a name is the text that is written.
func decodeMapping(root *yaml.Node) (model.Mapping, error) {
	var m model.Mapping
	sections, err := objectOf(root, "the file", sectionOrder)
	if err != nil {
		return model.Mapping{}, err
	}
	for _, s := range sections {
		switch s.key {
		case secInstruments:
			m.Instruments, err = decodeSection(s, decodeInstrument)
		case secAccounts:
			m.Accounts, err = decodeSection(s, decodeAccount)
		case secProjects:
			m.Projects, err = decodeSection(s, decodeProject)
		case secCategories:
			m.Expense, m.Income, err = decodeCategories(s)
		case secParties:
			m.Parties, err = decodeSection(s, field.text)
		case secTags:
			m.Tags, err = decodeSection(s, field.text)
		case secDefaults:
			m.Defaults, err = decodeDefaults(s)
		case secRows:
			m.Rows, err = decodeSection(s, decodeRow)
		}
		if err != nil {
			return model.Mapping{}, err
		}
	}
	return m, nil
}

// decodeSection reads a section, which is a mapping from a name to its entry, with
// decode, which makes the entry of the node of its value. A section that has no
// entry is nil.
func decodeSection[V any](section field, decode func(field) (V, error)) (map[string]V, error) {
	entries, err := entriesOf(section.val, section.key)
	if err != nil || len(entries) == 0 {
		return nil, err
	}
	out := make(map[string]V, len(entries))
	for _, e := range entries {
		v, err := decode(e)
		if err != nil {
			return nil, err
		}
		out[e.key] = v
	}
	return out, nil
}

func decodeInstrument(f field) (model.InstrumentMap, error) {
	var in model.InstrumentMap
	fields, err := objectOf(f.val, "an instrument", instrumentKeys)
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

func decodeAccount(f field) (model.AccountMap, error) {
	var a model.AccountMap
	fields, err := objectOf(f.val, "an account", accountKeys)
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

func decodeProject(f field) (model.ProjectMap, error) {
	var pm model.ProjectMap
	fields, err := objectOf(f.val, "a project", projectKeys)
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
			expense, err = decodeSection(s, decodeCategory)
		case secIncome:
			income, err = decodeSection(s, decodeCategory)
		}
		if err != nil {
			return nil, nil, err
		}
	}
	return expense, income, nil
}

// decodeCategory reads a category entry: the path of a ledger category alone, or the
// keys of what is made of the rows.
func decodeCategory(f field) (model.CategoryMap, error) {
	var c model.CategoryMap
	if f.val.Kind == yaml.ScalarNode {
		var err error
		c.Category, err = f.text()
		return c, err
	}
	fields, err := objectOf(f.val, "a category", categoryKeys)
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

func decodeRow(f field) (model.RowOverride, error) {
	var o model.RowOverride
	fields, err := objectOf(f.val, "a row", rowKeys)
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
// key; what is the name of the node is for the messages. No value is a mapping with
// no entries.
func entriesOf(n *yaml.Node, what string) ([]field, error) {
	n = resolved(n)
	if isNull(n) {
		return nil, nil
	}
	if n.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("line %d: %s must be a mapping", n.Line, what)
	}
	fields := make([]field, 0, len(n.Content)/2)
	lines := make(map[string]int, len(n.Content)/2) // the line each key is on
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := resolved(n.Content[i]), resolved(n.Content[i+1])
		if k.Kind != yaml.ScalarNode {
			return nil, fmt.Errorf("line %d: a key of %s must be a single value", k.Line, what)
		}
		if first, twice := lines[k.Value]; twice {
			return nil, fmt.Errorf("line %d: %s has the key %q twice, the first on line %d", k.Line, what, k.Value, first)
		}
		lines[k.Value] = k.Line
		fields = append(fields, field{key: k.Value, at: k, val: v})
	}
	return fields, nil
}

// objectOf lists the entries of a mapping node whose keys are only those of known.
func objectOf(n *yaml.Node, what string, known []string) ([]field, error) {
	fields, err := entriesOf(n, what)
	if err != nil {
		return nil, err
	}
	for _, f := range fields {
		if !slices.Contains(known, f.key) {
			return nil, fmt.Errorf("line %d: unknown key %q in %s; it has %s", f.at.Line, f.key, what, strings.Join(known, ", "))
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
		return "", fmt.Errorf("line %d: %s must be a single value", f.val.Line, f.key)
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
		return 0, fmt.Errorf("line %d: %s %q is not a whole number", f.val.Line, f.key, text)
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
	return false, fmt.Errorf("line %d: %s %q is not true or false", f.val.Line, f.key, text)
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
		return time.Time{}, fmt.Errorf("line %d: %s %q is not a day like 2026-04-15", f.val.Line, f.key, text)
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
		return nil, fmt.Errorf("line %d: %s %q is not a number", f.val.Line, f.key, text)
	}
	return &d, nil
}
