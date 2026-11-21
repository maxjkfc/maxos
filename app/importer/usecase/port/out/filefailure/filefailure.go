// Package filefailure declares what a file that the import reads or writes can fail
// with: the one error type of the out-ports source, mapping and rates. The file
// adapters return it, the use cases pass it on, and the command line says it in words.
//
// A failure is a code, where it is and the values the text of the code fills in, as a
// problem or a warning of a plan is: the library's own words are not part of it, and
// the words that the user reads are the command line's, so that all of it is in one
// language.
package filefailure

import (
	"fmt"
	"strings"
)

// Code says what is wrong with a file.
type Code string

// Failure is what is wrong with a file, or why it cannot be read or written. The file
// is in File, and the line of it in Line when the failure is about one; a "where" in
// Args is written as the subject of a problem is: the section of the mapping file
// ("accounts", "categories.expense"), or an entry of it ("accounts.Name"), and is empty
// for the file itself.
type Failure struct {
	Code Code
	// File is the file it is about, and Line the line of that file, from 1; a failure
	// that is not about a line has none.
	File string
	Line int
	// Args are the values the text of the code fills in; each code says which.
	Args []string
	// Err is what lies beneath, in its own words: the reason the system gives. The
	// command line says it as it is.
	Err error
}

// Error is for logs and tests: the code and what it is about. What the user reads is
// the command line's text of the code.
func (f *Failure) Error() string {
	var b strings.Builder
	b.WriteString(string(f.Code))
	if f.File != "" {
		fmt.Fprintf(&b, " file=%s", f.File)
	}
	if f.Line > 0 {
		fmt.Fprintf(&b, " line=%d", f.Line)
	}
	if len(f.Args) > 0 {
		fmt.Fprintf(&b, " args=%q", f.Args)
	}
	if f.Err != nil {
		fmt.Fprintf(&b, ": %v", f.Err)
	}
	return b.String()
}

// Unwrap gives the error beneath.
func (f *Failure) Unwrap() error { return f.Err }

// The files themselves.
const (
	// Missing is an export that is not there.
	Missing Code = "file_missing"
	// Unreadable is a file that cannot be read. Err is the system's reason.
	Unreadable Code = "file_unreadable"
	// NotWritten is a file that cannot be written: the mapping, which is the one file
	// the import writes. Err is the system's reason.
	NotWritten Code = "file_not_written"
	// Empty is a CSV file that has nothing in it, not even a header.
	Empty Code = "file_empty"
)

// The CSV files: the export and the rates.
const (
	// ColumnTwice is a header that has a column that is used twice. Line: the header.
	// Args: the column.
	ColumnTwice Code = "column_twice"
	// ColumnsMissing is a header that lacks columns that are needed. Line: the header.
	// Args: the columns, in the order they are needed.
	ColumnsMissing Code = "columns_missing"
	// FieldCount is a record that has not as many fields as the header. Args: how many
	// it has, and how many the header has.
	FieldCount Code = "field_count"
	// CSVBareQuote is a quote in a field that is not in quotes. Args: none, or the line
	// the record begins on when that is not Line.
	CSVBareQuote Code = "csv_bare_quote"
	// CSVQuote is a quote that is opened and not closed, or a field in quotes that goes
	// on after it is closed. Args: none, or the line the record begins on when that is
	// not Line.
	CSVQuote Code = "csv_quote"
	// CSVSyntax is a CSV file that is wrong in another way. Args: none, or the line the
	// record begins on when that is not Line.
	CSVSyntax Code = "csv_syntax"
	// RateNotPositive is a rate that is not above zero. Args: the column, and the text.
	RateNotPositive Code = "rate_not_positive"
	// RateTwice is a currency that has two rates for a day. Args: the code, the day, and
	// the line of the first.
	RateTwice Code = "rate_twice"
)

// The values of the files, of the CSV files and of the mapping file.
const (
	// ValueEmpty is a value that has to be there and is not. Args: the column or the
	// key.
	ValueEmpty Code = "value_empty"
	// NotNumber is a value that is not a number. Args: the column or the key, and the
	// text.
	NotNumber Code = "not_number"
	// NotWholeNumber is a value that is not a whole number. Args: the key, and the text.
	NotWholeNumber Code = "not_whole_number"
	// NotBoolean is a value that is not true or false. Args: the key, and the text.
	NotBoolean Code = "not_boolean"
	// NotDay is a value that is not a day. Args: the column or the key, the text, and a
	// day written as it is to be.
	NotDay Code = "not_day"
	// NotTime is a value that is not a time of day. Args: the column, and the text.
	NotTime Code = "not_time"
)

// The mapping file, as YAML.
const (
	// YAMLSyntax is a mapping file that is not YAML. Line is that of the problem, and
	// zero when the YAML library does not say.
	YAMLSyntax Code = "yaml_syntax"
	// SecondDocument is a mapping file that has a second YAML document. Line: where it
	// begins.
	SecondDocument Code = "second_document"
	// NotSections is a mapping file that is not a mapping of sections.
	NotSections Code = "not_sections"
	// MustBeMapping is a section or an entry that is not a mapping, which it has to be.
	// Args: where.
	MustBeMapping Code = "must_be_mapping"
	// KeyNotSingle is a key that is not one value. Args: where.
	KeyNotSingle Code = "key_not_single"
	// ValueNotSingle is a value that is not one value, which it has to be. Args: the
	// key.
	ValueNotSingle Code = "value_not_single"
	// KeyTwice is a key that a mapping has twice. Line: the second. Args: where, the
	// key, and the line of the first.
	KeyTwice Code = "key_twice"
	// KeyUnknown is a key that the mapping file does not know. Args: the key, where, and
	// then the keys that are known there.
	KeyUnknown Code = "key_unknown"
)

// Adding entries to the mapping file.
const (
	// EntryNotWritable is an entry that cannot be written to the mapping file as a line
	// of it. Args: its section and its key.
	EntryNotWritable Code = "entry_not_writable"
	// Braces is a section, or the file, that is written between braces with entries in
	// it, which lines cannot be added to. Line: where it begins. Args: where, which is
	// empty for the whole file.
	Braces Code = "braces"
	// Layout is a section that is laid out so that lines cannot be added to it. Line: its
	// key. Args: where.
	Layout Code = "layout"
	// WouldReadDifferently is a mapping file that cannot be added to as it is laid out:
	// what it would say of an entry is not what it says now, or not what was asked, and
	// nothing is written. Args: the section and the key of the first entry that would
	// differ, or none when the new text is not even read.
	WouldReadDifferently Code = "would_read_differently"
)
