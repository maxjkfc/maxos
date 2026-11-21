package file

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/maxjkfc/maxos/app/importer/domain/model"
)

const (
	// indentStep is how far in the lines of a section are from its key, in a section
	// that has none yet.
	indentStep = 2

	// The paths of the two sections of categories, as additions name them and as the
	// model names the entries of a mapping.
	pathExpense = model.SectionExpense
	pathIncome  = model.SectionIncome
)

// newFileHeader is what a mapping file begins with when the import creates it. It
// must not say "# guessed" anywhere: that is what Load counts.
const newFileHeader = `# maxos import moze 的對應表：MOZE 匯出檔裡的帳戶、類別、專案、對象與標籤，在帳本裡各是什麼。
#
# 行尾註解為 guessed 的項目是程式猜的：請逐項確認，要改就直接改，確認後把行尾的註解刪掉。
# 程式只會補上檔案裡還沒有的項目；你寫的內容、註解和順序都不會被動到。
#
# 個別幾列有例外時，把那一列的 key 寫在 rows: 底下覆寫它，例如：
#   rows:
#     abc123-1: {treatment: dealing, party: Zed}
#     abc123-2: {quantity: "0.0123"}
# treatment 可以是 income、expense、reward、dealing、skip。
`

// Append adds the entries of guessed that the file at path does not have, each on a
// line of its own that ends with "# guessed", to the section it belongs to, in the
// order of their keys.
//
// What the file has is not touched, not a character of it: the new lines go right
// after the last line of the entries of their section, indented as those are, and a
// section the file lacks is added at its end. An entry whose key the section has is
// not added again, so that a second Append of the same is nothing. When there is
// nothing to add the file is not written, and a file that does not exist is not
// made.
//
// A file that does not exist is created, with its folders, and begins with a comment
// that says what it is. A file is replaced in one step, with the permissions it had,
// and the new lines end the way its lines do.
//
// Before anything is written the new text is read back, and what it says of every
// entry has to be what the file said, and, of the entries put in, what was asked,
// value for value. When it is not, Append returns an error that names the first entry
// that would read differently and writes nothing. That is how a layout it cannot put
// lines into is found out: a block scalar that has a line like a comment in it, which
// a new entry would cut short.
//
// A section that is written between braces with entries in it, {a: b}, cannot be
// added to: that is an error, and the file is left as it is. {} with nothing in it
// is made the key of a section of lines.
func (*MappingYAML) Append(ctx context.Context, path string, guessed model.Mapping) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if target, err := filepath.EvalSymlinks(path); err == nil {
		path = target // a link to the file stays a link, and the file it points to is written
	}

	data, perm, err := readFile(path)
	created := errors.Is(err, fs.ErrNotExist)
	switch {
	case created:
		data, perm = []byte(newFileHeader), 0o600
	case err != nil:
		return err
	}

	updated, changed, err := extend(data, guessed)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if !changed {
		return nil
	}
	if created {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return err
		}
	}
	return writeFileAtomic(path, updated, perm)
}

// readFile reads a file and returns its permission bits.
func readFile(path string) ([]byte, fs.FileMode, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return nil, 0, err
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, 0, err
	}
	return data, info.Mode().Perm(), nil
}

// writeFileAtomic writes the data to a new file in the folder of path and renames
// that over path, so that nobody sees the file half written.
func writeFileAtomic(path string, data []byte, perm fs.FileMode) (err error) {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
		}
	}()
	if _, err = tmp.Write(data); err != nil {
		return err
	}
	if err = tmp.Chmod(perm); err != nil {
		return err
	}
	if err = tmp.Sync(); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// entry is an entry to write.
type entry struct {
	key string
	// text is the entry as one line of YAML, "key: value", with no indentation.
	text string
	// err is why the entry cannot be written, which is an error only when it has to
	// be: an entry that the file has already is not written.
	err error
}

// additions are the entries to add, by the path of their section: "accounts", or
// "categories.expense".
type additions map[string][]entry

// renderAdditions writes every entry of guessed, each section in the order of its
// keys.
func renderAdditions(g model.Mapping) additions {
	return additions{
		secInstruments: renderAll(g.Instruments, renderInstrument),
		secAccounts:    renderAll(g.Accounts, renderAccount),
		secProjects:    renderAll(g.Projects, renderProject),
		pathExpense:    renderAll(g.Expense, renderCategory),
		pathIncome:     renderAll(g.Income, renderCategory),
		secParties:     renderAll(g.Parties, renderName),
		secTags:        renderAll(g.Tags, renderName),
		secDefaults:    renderDefaults(g.Defaults),
		secRows:        renderAll(g.Rows, renderRow),
	}
}

// pick returns the entries of the section at path whose keys are not in have: what
// is to be added, or the error of the first of them that cannot be written.
func (a additions) pick(path string, have map[string]bool) ([]entry, error) {
	var out []entry
	for _, e := range a[path] {
		if have[e.key] {
			continue
		}
		if e.err != nil {
			return nil, fmt.Errorf("%s %q: %w", path, e.key, e.err)
		}
		out = append(out, e)
	}
	return out, nil
}

// renderAll writes the entries of a section in the order of their keys.
func renderAll[V any](entries map[string]V, render func(key string, v V) (string, error)) []entry {
	out := make([]entry, 0, len(entries))
	for _, key := range slices.Sorted(maps.Keys(entries)) {
		text, err := render(key, entries[key])
		out = append(out, entry{key: key, text: text, err: err})
	}
	return out
}

// renderInstrument writes an instrument. Its kind, is_currency and decimals are
// always written, the rest when it has them.
func renderInstrument(code string, in model.InstrumentMap) (string, error) {
	m := newFlow()
	putText(m, keyName, in.Name)
	put(m, keyKind, textNode(in.Kind))
	put(m, keyIsCurrency, scalarNode("!!bool", strconv.FormatBool(in.IsCurrency)))
	put(m, keyDecimals, scalarNode("!!int", strconv.Itoa(in.Decimals)))
	putText(m, keyRateAs, in.RateAs)
	return entryLine(code, m)
}

// renderAccount writes an account, with what it has of the keys; an opening date is
// written as a date, which is not quoted.
func renderAccount(name string, a model.AccountMap) (string, error) {
	m := newFlow()
	putText(m, keyName, a.Name)
	putText(m, keyKind, a.Kind)
	putText(m, keyInstitution, a.Institution)
	putInt(m, keyClosingDay, a.ClosingDay)
	putInt(m, keyPaymentDueDay, a.PaymentDueDay)
	putDay(m, keyOpeningDate, a.OpeningDate)
	return entryLine(name, m)
}

// renderProject writes a project, with what it has of the keys.
func renderProject(value string, p model.ProjectMap) (string, error) {
	m := newFlow()
	putText(m, keyKind, p.Kind)
	putText(m, keyName, p.Name)
	putText(m, keyDefaultArea, p.DefaultArea)
	putText(m, keyParty, p.Party)
	return entryLine(value, m)
}

// renderCategory writes a category entry: the path of the ledger category alone when
// that is all it is, or the keys it has.
func renderCategory(key string, c model.CategoryMap) (string, error) {
	if c.Treatment == "" && c.Party == "" && c.Tag == "" {
		return entryLine(key, textNode(c.Category))
	}
	m := newFlow()
	putText(m, keyCategory, c.Category)
	putText(m, keyTreatment, c.Treatment)
	putText(m, keyParty, c.Party)
	putText(m, keyTag, c.Tag)
	return entryLine(key, m)
}

// renderName writes a party or a tag: the name MOZE has, and the one of the ledger.
func renderName(key, name string) (string, error) {
	return entryLine(key, textNode(name))
}

// renderRow writes the override of a row, with what it has of the keys. The quantity
// is text, which is what keeps it from being read as a float.
func renderRow(key string, o model.RowOverride) (string, error) {
	m := newFlow()
	putText(m, keyTreatment, o.Treatment)
	putText(m, keyCategory, o.Category)
	putText(m, keyParty, o.Party)
	putText(m, keyTag, o.Tag)
	if o.Quantity != nil {
		put(m, keyQuantity, textNode(o.Quantity.String()))
	}
	return entryLine(key, m)
}

// defaultFields lists the keys of the defaults with what each holds.
func defaultFields(d model.Defaults) []struct{ key, value string } {
	return []struct{ key, value string }{
		{keyFeeCategory, d.FeeCategory},
		{keyInterestCategory, d.InterestCategory},
		{keyRefundCategory, d.RefundCategory},
	}
}

// renderDefaults writes the defaults that are set, each as an entry of its own.
func renderDefaults(d model.Defaults) []entry {
	var out []entry
	for _, f := range defaultFields(d) {
		if f.value == "" {
			continue
		}
		text, err := entryLine(f.key, textNode(f.value))
		out = append(out, entry{key: f.key, text: text, err: err})
	}
	return out
}

// entryLine writes "key: value" the way the YAML encoder does, which quotes what
// has to be quoted: a key such as 2025, true, "a: b" or "#x" stays the text it
// is, and so does a value inside a flow mapping, which has more to be quoted for
// than a key has. What cannot be written on one line, a key of more than 128
// characters or with a line break in it, is an error.
func entryLine(key string, value *yaml.Node) (string, error) {
	out, err := yaml.Marshal(&yaml.Node{
		Kind:    yaml.MappingNode,
		Tag:     "!!map",
		Content: []*yaml.Node{textNode(key), value},
	})
	if err != nil {
		return "", err
	}
	line := strings.TrimSuffix(string(out), "\n")
	if strings.Contains(line, "\n") {
		return "", errors.New("it cannot be written on one line")
	}
	return line, nil
}

// newFlow returns a mapping node that is written between braces, on one line.
func newFlow() *yaml.Node {
	return &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Style: yaml.FlowStyle}
}

// put adds a key and its value to a mapping node.
func put(m *yaml.Node, key string, value *yaml.Node) {
	m.Content = append(m.Content, textNode(key), value)
}

// putText adds a key with a text, unless the text is empty.
func putText(m *yaml.Node, key, text string) {
	if text != "" {
		put(m, key, textNode(text))
	}
}

// putInt adds a key with a number, unless it is zero.
func putInt(m *yaml.Node, key string, n int) {
	if n != 0 {
		put(m, key, scalarNode("!!int", strconv.Itoa(n)))
	}
}

// putDay adds a key with a day, written as a date, unless it is the zero time.
func putDay(m *yaml.Node, key string, day time.Time) {
	if !day.IsZero() {
		put(m, key, scalarNode("!!timestamp", day.Format(time.DateOnly)))
	}
}

// textNode is a text, which the encoder quotes when it would be read as something
// else, a number or a truth.
func textNode(text string) *yaml.Node { return scalarNode("!!str", text) }

// scalarNode is a value of the tag; the encoder writes it as it is when the value
// reads as that tag without one, a day as a date or 2 as a number.
func scalarNode(tag, value string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: tag, Value: value}
}

// extend adds the entries of guessed that the text of a mapping file does not have, and
// says whether there was anything to add: when there was not, the text is as it was.
func extend(data []byte, guessed model.Mapping) (updated []byte, changed bool, err error) {
	doc, err := parseDocument(data)
	if err != nil {
		return nil, false, err
	}
	ed, err := newEditor(data, doc.root)
	if err != nil {
		return nil, false, err
	}
	adds := renderAdditions(guessed)
	for _, name := range sectionOrder {
		if name == secCategories {
			err = ed.categories(adds)
		} else {
			err = ed.section(name, adds)
		}
		if err != nil {
			return nil, false, err
		}
	}
	if len(ed.added) == 0 {
		return data, false, nil
	}
	if ed.flowRoot {
		return nil, false, errors.New("the file is written between braces, {...}; write its sections as lines so that entries can be added")
	}
	updated = ed.text()

	// What is written has to read back as the file with the entries put in, whatever
	// the layout of the file, and value for value: a layout that the editing does not
	// understand is an error here, with the file as it was, and not a file whose
	// entries have lost what they said.
	const cannot = "the entries cannot be added to the file as it is laid out; nothing is written"
	back, err := parseDocument(updated)
	if err != nil {
		return nil, false, fmt.Errorf("%s (%w)", cannot, err)
	}
	want := doc.content.Merge(addedOf(guessed, ed.added))
	if ref, differs := firstDifference(back.content, want); differs {
		return nil, false, fmt.Errorf("%s (%s would read differently)", cannot, ref)
	}
	return updated, true, nil
}

// addedOf returns the entries of guessed that are put in, which are the ones that
// the file did not have: what the mapping the file says gains.
func addedOf(guessed model.Mapping, added []model.EntryRef) model.Mapping {
	set := make(map[model.EntryRef]bool, len(added))
	for _, ref := range added {
		set[ref] = true
	}
	var defaults model.Defaults
	if set[model.EntryRef{Section: model.SectionDefaults, Key: keyFeeCategory}] {
		defaults.FeeCategory = guessed.Defaults.FeeCategory
	}
	if set[model.EntryRef{Section: model.SectionDefaults, Key: keyInterestCategory}] {
		defaults.InterestCategory = guessed.Defaults.InterestCategory
	}
	if set[model.EntryRef{Section: model.SectionDefaults, Key: keyRefundCategory}] {
		defaults.RefundCategory = guessed.Defaults.RefundCategory
	}
	return model.Mapping{
		Instruments: only(model.SectionInstruments, guessed.Instruments, set),
		Accounts:    only(model.SectionAccounts, guessed.Accounts, set),
		Projects:    only(model.SectionProjects, guessed.Projects, set),
		Expense:     only(model.SectionExpense, guessed.Expense, set),
		Income:      only(model.SectionIncome, guessed.Income, set),
		Parties:     only(model.SectionParties, guessed.Parties, set),
		Tags:        only(model.SectionTags, guessed.Tags, set),
		Defaults:    defaults,
		Rows:        only(model.SectionRows, guessed.Rows, set),
	}
}

// only returns the entries of a section that are in the set.
func only[V any](section string, entries map[string]V, set map[model.EntryRef]bool) map[string]V {
	out := make(map[string]V, len(entries))
	for key, v := range entries {
		if set[model.EntryRef{Section: section, Key: key}] {
			out[key] = v
		}
	}
	return out
}

// firstDifference returns the first entry, section by section as Mapping.Items has
// them, that the two mappings do not have the same way: one has it and the other does
// not, or it says another thing. It tells whether there is one.
func firstDifference(got, want model.Mapping) (model.EntryRef, bool) {
	if ref, ok := differing(model.SectionInstruments, got.Instruments, want.Instruments, equal); ok {
		return ref, true
	}
	if ref, ok := differing(model.SectionAccounts, got.Accounts, want.Accounts, sameAccount); ok {
		return ref, true
	}
	if ref, ok := differing(model.SectionProjects, got.Projects, want.Projects, equal); ok {
		return ref, true
	}
	if ref, ok := differing(model.SectionExpense, got.Expense, want.Expense, equal); ok {
		return ref, true
	}
	if ref, ok := differing(model.SectionIncome, got.Income, want.Income, equal); ok {
		return ref, true
	}
	if ref, ok := differing(model.SectionParties, got.Parties, want.Parties, equal); ok {
		return ref, true
	}
	if ref, ok := differing(model.SectionTags, got.Tags, want.Tags, equal); ok {
		return ref, true
	}
	g, w := defaultFields(got.Defaults), defaultFields(want.Defaults)
	for i := range w {
		if g[i].value != w[i].value {
			return model.EntryRef{Section: model.SectionDefaults, Key: w[i].key}, true
		}
	}
	return differing(model.SectionRows, got.Rows, want.Rows, sameRow)
}

// differing returns the first entry of a section, in the order of the keys, that the
// two maps do not have the same way: what want has, then what only got has.
func differing[V any](section string, got, want map[string]V, same func(a, b V) bool) (model.EntryRef, bool) {
	for _, key := range slices.Sorted(maps.Keys(want)) {
		if g, ok := got[key]; !ok || !same(g, want[key]) {
			return model.EntryRef{Section: section, Key: key}, true
		}
	}
	for _, key := range slices.Sorted(maps.Keys(got)) {
		if _, ok := want[key]; !ok {
			return model.EntryRef{Section: section, Key: key}, true
		}
	}
	return model.EntryRef{}, false
}

func equal[V comparable](a, b V) bool { return a == b }

// sameAccount tells whether two accounts say the same. An opening date is a day: two
// times of the same instant are the same, whatever zone they are held in.
func sameAccount(a, b model.AccountMap) bool {
	ad, bd := a.OpeningDate, b.OpeningDate
	a.OpeningDate, b.OpeningDate = time.Time{}, time.Time{}
	return a == b && ad.Equal(bd)
}

// sameRow tells whether two overrides say the same. A quantity is a number: 1.50 and
// 1.5 are the same, though the decimals that hold them are not equal as values.
func sameRow(a, b model.RowOverride) bool {
	aq, bq := a.Quantity, b.Quantity
	a.Quantity, b.Quantity = nil, nil
	return a == b && (aq == nil) == (bq == nil) && (aq == nil || aq.Equal(*bq))
}

// editor adds lines to the text of a file, and leaves the rest of it as it is.
type editor struct {
	lines []string // the lines of the file, each with its line ending
	eol   string   // the line ending of the lines that are added
	top   int      // how far in the keys of the sections are

	places    map[string]field // the sections the file has, by key
	topStarts []int            // the index of the line each section begins on
	allStarts []int            // the same, with the two sections of categories

	after   map[int][]string // the lines to add after the line of the index
	changed map[int]string   // the lines that are written another way
	tail    [][]string       // the sections to add at the end of the file
	added   []model.EntryRef // the entries that are to be added

	flowRoot bool // the whole file is one mapping between braces, which lines cannot be added to
}

// newEditor sets up an editor for the text of a file whose parsed form is root.
func newEditor(data []byte, root *yaml.Node) (*editor, error) {
	ed := &editor{
		lines:   splitLines(data),
		eol:     lineEnding(data),
		places:  map[string]field{},
		after:   map[int][]string{},
		changed: map[int]string{},
	}
	if root == nil {
		return ed, nil
	}
	ed.flowRoot = root.Style&yaml.FlowStyle != 0
	sections, err := entriesOf(root, "the file")
	if err != nil {
		return nil, err
	}
	if len(root.Content) > 0 {
		ed.top = root.Content[0].Column - 1
	}
	for _, s := range sections {
		ed.places[s.key] = s
		ed.topStarts = append(ed.topStarts, s.at.Line-1)
		ed.allStarts = append(ed.allStarts, s.at.Line-1)
		if s.key != secCategories || shapeOf(s.val) != shapeBlock {
			continue
		}
		children, err := entriesOf(s.val, s.key)
		if err != nil {
			return nil, err
		}
		for _, c := range children {
			ed.allStarts = append(ed.allStarts, c.at.Line-1)
		}
	}
	return ed, nil
}

// section adds the entries of a section that has no sections in it.
func (ed *editor) section(name string, adds additions) error {
	s, has := ed.places[name]
	var have map[string]bool
	if has {
		have = keysOf(s.val)
	}
	entries, err := adds.pick(name, have)
	if err != nil || len(entries) == 0 {
		return err
	}
	if has {
		after, indent, err := ed.room(s, ed.topStarts)
		if err != nil {
			return err
		}
		ed.addAfter(after, entryLines(indent, entries)...)
	} else {
		ed.tail = append(ed.tail, append([]string{spaces(ed.top) + name + ":"}, entryLines(ed.top+indentStep, entries)...))
	}
	ed.record(name, entries)
	return nil
}

// categories adds the entries of the two sections of categories. A section the file
// has gets its entries like any other; one it lacks is added to the end of the
// categories, which are added to the end of the file when it lacks them too.
func (ed *editor) categories(adds additions) error {
	cats, has := ed.places[secCategories]
	children := map[string]field{}
	if has && shapeOf(cats.val) == shapeBlock {
		fields, err := entriesOf(cats.val, cats.key)
		if err != nil {
			return err
		}
		for _, c := range fields {
			children[c.key] = c
		}
	}

	type group struct {
		name, path string
		entries    []entry
	}
	var missing []group
	for _, name := range categorySections {
		path := secCategories + "." + name
		child, exists := children[name]
		var have map[string]bool
		if exists {
			have = keysOf(child.val)
		}
		entries, err := adds.pick(path, have)
		if err != nil {
			return err
		}
		switch {
		case len(entries) == 0:
		case exists:
			after, indent, err := ed.room(child, ed.allStarts)
			if err != nil {
				return err
			}
			ed.addAfter(after, entryLines(indent, entries)...)
			ed.record(path, entries)
		default:
			missing = append(missing, group{name: name, path: path, entries: entries})
		}
	}
	if len(missing) == 0 {
		return nil
	}

	// linesAt writes the sections that are missing: their keys are as far in as
	// keyIndent, and their entries as far in as entryIndent.
	linesAt := func(keyIndent, entryIndent int) []string {
		var lines []string
		for _, g := range missing {
			lines = append(lines, spaces(keyIndent)+g.name+":")
			lines = append(lines, entryLines(entryIndent, g.entries)...)
		}
		return lines
	}
	if has {
		after, indent, err := ed.room(cats, ed.topStarts)
		if err != nil {
			return err
		}
		// The entries are written as far in as those of a section the file has, when
		// there is one.
		entryIndent := indent + indentStep
		for _, name := range categorySections {
			if c, ok := children[name]; ok && shapeOf(c.val) == shapeBlock {
				entryIndent = c.val.Content[0].Column - 1
				break
			}
		}
		ed.addAfter(after, linesAt(indent, entryIndent)...)
	} else {
		ed.tail = append(ed.tail, append([]string{spaces(ed.top) + secCategories + ":"},
			linesAt(ed.top+indentStep, ed.top+2*indentStep)...))
	}
	for _, g := range missing {
		ed.record(g.path, g.entries)
	}
	return nil
}

// room finds where the lines of a section that the file has are added: the index of
// the line they come after, and how far in they are written. It makes a section
// that has no entries, and has {} or ~ written beside its key, into the key alone,
// to be followed by its lines. starts are where the sections that can follow it
// begin.
func (ed *editor) room(s field, starts []int) (after, indent int, err error) {
	switch shapeOf(s.val) {
	case shapeEmpty:
		if err := ed.clearValue(s); err != nil {
			return 0, 0, err
		}
		return s.at.Line - 1, s.at.Column - 1 + indentStep, nil
	case shapeBlock:
		return ed.contentEnd(s, starts), s.val.Content[0].Column - 1, nil
	}
	return 0, 0, fmt.Errorf("line %d: %s is written between braces, {...}; write its entries as lines so that entries can be added", s.at.Line, s.key)
}

// contentEnd is the index of the last line of a section that has something on it:
// not a blank line, not one that is only a comment, and not past where the next
// section begins.
func (ed *editor) contentEnd(s field, starts []int) int {
	key := s.at.Line - 1
	limit := len(ed.lines)
	for _, start := range starts {
		if start > key {
			limit = start
			break
		}
	}
	end := key
	for i := limit - 1; i > key; i-- {
		if t := strings.TrimSpace(ed.lines[i]); t != "" && !strings.HasPrefix(t, "#") {
			end = i
			break
		}
	}
	// A value of several lines is never cut short, whatever its lines look like.
	return max(end, lastLine(s.val)-1)
}

// lastLine is the last line that a node, or a node under it, begins on.
func lastLine(n *yaml.Node) int {
	last := n.Line
	for _, c := range n.Content {
		last = max(last, lastLine(c))
	}
	return last
}

// clearValue rewrites the line of a section that has an empty value beside its key,
// "accounts: {}" or "accounts: ~", as the key alone, "accounts:". A comment at the
// end of the line stays.
func (ed *editor) clearValue(s field) error {
	if s.val.Kind == yaml.ScalarNode && s.val.Value == "" {
		return nil // nothing is written beside the key
	}
	i := s.at.Line - 1
	line := ed.lines[i]
	start := s.val.Column - 1
	if s.val.Line != s.at.Line || start < 0 || start > len(line) {
		return layoutError(s)
	}
	rest := line[start:]
	var width int
	switch {
	case s.val.Kind == yaml.ScalarNode && strings.HasPrefix(rest, s.val.Value):
		width = len(s.val.Value)
	case s.val.Kind == yaml.MappingNode && strings.HasPrefix(rest, "{"):
		end := strings.IndexByte(rest, '}')
		if end < 0 || strings.TrimSpace(rest[1:end]) != "" {
			return layoutError(s)
		}
		width = end + 1
	default:
		return layoutError(s)
	}
	head := strings.TrimRight(line[:start], " ")
	tail := line[start+width:]
	if strings.TrimSpace(tail) == "" {
		tail = lineBreak(line)
	}
	ed.changed[i] = head + tail
	return nil
}

func layoutError(s field) error {
	return fmt.Errorf("line %d: cannot add entries to %s as it is written; write its entries as lines below its key", s.at.Line, s.key)
}

// addAfter adds lines after the line of the index, after those that are there.
func (ed *editor) addAfter(i int, lines ...string) {
	ed.after[i] = append(ed.after[i], lines...)
}

// record notes the entries of a section as added; the path is the name of the section
// that the model gives its entries.
func (ed *editor) record(path string, entries []entry) {
	for _, e := range entries {
		ed.added = append(ed.added, model.EntryRef{Section: path, Key: e.key})
	}
}

// text is the text of the file with the lines in it. A last line that has no line
// ending gets one before anything follows it, and a section added at the end is
// set off by a blank line.
func (ed *editor) text() []byte {
	var b strings.Builder
	last := len(ed.lines) - 1
	for i, line := range ed.lines {
		if changed, ok := ed.changed[i]; ok {
			line = changed
		}
		b.WriteString(line)
		more := ed.after[i]
		if (len(more) > 0 || (i == last && len(ed.tail) > 0)) && !strings.HasSuffix(line, "\n") {
			b.WriteString(ed.eol)
		}
		for _, l := range more {
			b.WriteString(l)
			b.WriteString(ed.eol)
		}
	}
	blank := last >= 0 && strings.TrimSpace(ed.lines[last]) != ""
	for _, section := range ed.tail {
		if blank {
			b.WriteString(ed.eol)
		}
		blank = true
		for _, l := range section {
			b.WriteString(l)
			b.WriteString(ed.eol)
		}
	}
	return []byte(b.String())
}

// entryLines writes the lines of entries, each marked as guessed, indented.
func entryLines(indent int, entries []entry) []string {
	lines := make([]string, len(entries))
	for i, e := range entries {
		lines[i] = spaces(indent) + e.text + "  " + guessedMark
	}
	return lines
}

func spaces(n int) string { return strings.Repeat(" ", n) }

// shape is how a section has its entries written.
type shape int

const (
	shapeEmpty shape = iota // it has none: nothing, ~ or {}
	shapeBlock              // each on a line of its own
	shapeFlow               // between braces
)

func shapeOf(n *yaml.Node) shape {
	switch {
	case isNull(n), n.Kind == yaml.MappingNode && len(n.Content) == 0:
		return shapeEmpty
	case n.Kind == yaml.MappingNode && n.Style&yaml.FlowStyle == 0:
		return shapeBlock
	}
	return shapeFlow
}

// keysOf returns the keys of a mapping node.
func keysOf(n *yaml.Node) map[string]bool {
	keys := map[string]bool{}
	if n.Kind != yaml.MappingNode {
		return keys
	}
	for i := 0; i < len(n.Content); i += 2 {
		keys[resolved(n.Content[i]).Value] = true
	}
	return keys
}

// splitLines splits text into its lines, each with its line ending.
func splitLines(data []byte) []string {
	var lines []string
	for line := range bytes.Lines(data) {
		lines = append(lines, string(line))
	}
	return lines
}

// lineEnding is the line ending the file begins its lines with: "\r\n" or "\n".
func lineEnding(data []byte) string {
	if i := bytes.IndexByte(data, '\n'); i > 0 && data[i-1] == '\r' {
		return "\r\n"
	}
	return "\n"
}

// lineBreak is the line ending that a line has, which is empty for a last line that
// has none.
func lineBreak(line string) string {
	switch {
	case strings.HasSuffix(line, "\r\n"):
		return "\r\n"
	case strings.HasSuffix(line, "\n"):
		return "\n"
	}
	return ""
}
