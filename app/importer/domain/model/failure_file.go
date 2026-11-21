package model

// The codes of the failures that are about the files the import reads and writes: the
// export, the rates and the mapping. A failure is a code, where it is, and the values
// its text fills in (see Failure); each code says which values it has in Args.
//
// A failure that is about a file has the file in File, and one that is about a line
// of it has the line in Line. A name of a part of the mapping file, a "where" in what
// follows, is written as the subject of a problem is: the section ("accounts",
// "categories.expense"), or an entry of it ("accounts.Name"); it is empty for the
// file itself.
//
// Of what the user is told, the library's own words are not: a failure says what is
// wrong and the command line says it, so that all of it is in one language.
const (
	// FailureFileMissing is an export that is not there.
	FailureFileMissing FailureCode = "file_missing"
	// FailureFileUnreadable is a file that cannot be read. Err is the system's reason.
	FailureFileUnreadable FailureCode = "file_unreadable"
	// FailureFileNotWritten is a file that cannot be written: the mapping, which is
	// the one file the import writes. Err is the system's reason.
	FailureFileNotWritten FailureCode = "file_not_written"
	// FailureFileEmpty is a CSV file that has nothing in it, not even a header.
	FailureFileEmpty FailureCode = "file_empty"

	// FailureColumnTwice is a header that has a column that is used twice. Line: the
	// header. Args: the column.
	FailureColumnTwice FailureCode = "column_twice"
	// FailureColumnsMissing is a header that lacks columns that are needed. Line: the
	// header. Args: the columns, in the order they are needed.
	FailureColumnsMissing FailureCode = "columns_missing"
	// FailureFieldCount is a record that has not as many fields as the header. Args:
	// how many it has, and how many the header has.
	FailureFieldCount FailureCode = "field_count"
	// FailureCSVBareQuote is a quote in a field that is not in quotes. Args: none, or
	// the line the record begins on when that is not Line.
	FailureCSVBareQuote FailureCode = "csv_bare_quote"
	// FailureCSVQuote is a quote that is opened and not closed, or a field in quotes
	// that goes on after it is closed. Args: none, or the line the record begins on
	// when that is not Line.
	FailureCSVQuote FailureCode = "csv_quote"
	// FailureCSVSyntax is a CSV file that is wrong in another way. Args: none, or the
	// line the record begins on when that is not Line.
	FailureCSVSyntax FailureCode = "csv_syntax"

	// FailureValueEmpty is a value that has to be there and is not. Args: the column
	// or the key.
	FailureValueEmpty FailureCode = "value_empty"
	// FailureNotNumber is a value that is not a number. Args: the column or the key,
	// and the text.
	FailureNotNumber FailureCode = "not_number"
	// FailureNotWholeNumber is a value that is not a whole number. Args: the key, and
	// the text.
	FailureNotWholeNumber FailureCode = "not_whole_number"
	// FailureNotBoolean is a value that is not true or false. Args: the key, and the
	// text.
	FailureNotBoolean FailureCode = "not_boolean"
	// FailureNotDay is a value that is not a day. Args: the column or the key, the
	// text, and a day written as it is to be.
	FailureNotDay FailureCode = "not_day"
	// FailureNotTime is a value that is not a time of day. Args: the column, and the
	// text.
	FailureNotTime FailureCode = "not_time"
	// FailureRateNotPositive is a rate that is not above zero. Args: the column, and
	// the text.
	FailureRateNotPositive FailureCode = "rate_not_positive"
	// FailureRateTwice is a currency that has two rates for a day. Args: the code, the
	// day, and the line of the first.
	FailureRateTwice FailureCode = "rate_twice"

	// FailureYAMLSyntax is a mapping file that is not YAML. Line is that of the
	// problem, and zero when the YAML library does not say.
	FailureYAMLSyntax FailureCode = "yaml_syntax"
	// FailureSecondDocument is a mapping file that has a second YAML document. Line:
	// where it begins.
	FailureSecondDocument FailureCode = "second_document"
	// FailureNotSections is a mapping file that is not a mapping of sections.
	FailureNotSections FailureCode = "not_sections"
	// FailureMustBeMapping is a section or an entry that is not a mapping, which it has
	// to be. Args: where.
	FailureMustBeMapping FailureCode = "must_be_mapping"
	// FailureKeyNotSingle is a key that is not one value. Args: where.
	FailureKeyNotSingle FailureCode = "key_not_single"
	// FailureValueNotSingle is a value that is not one value, which it has to be. Args:
	// the key.
	FailureValueNotSingle FailureCode = "value_not_single"
	// FailureKeyTwice is a key that a mapping has twice. Line: the second. Args: where,
	// the key, and the line of the first.
	FailureKeyTwice FailureCode = "key_twice"
	// FailureKeyUnknown is a key that the mapping file does not know. Args: the key,
	// where, and then the keys that are known there.
	FailureKeyUnknown FailureCode = "key_unknown"

	// FailureEntryNotWritable is an entry that cannot be written to the mapping file
	// as a line of it. Args: its section and its key.
	FailureEntryNotWritable FailureCode = "entry_not_writable"
	// FailureBraces is a section, or the file, that is written between braces with
	// entries in it, which lines cannot be added to. Line: where it begins. Args:
	// where, which is empty for the whole file.
	FailureBraces FailureCode = "braces"
	// FailureLayout is a section that is laid out so that lines cannot be added to it.
	// Line: its key. Args: where.
	FailureLayout FailureCode = "layout"
	// FailureWouldReadDifferently is a mapping file that cannot be added to as it is
	// laid out: what it would say of an entry is not what it says now, or not what
	// was asked, and nothing is written. Args: the section and the key of the first
	// entry that would differ, or none when the new text is not even read.
	FailureWouldReadDifferently FailureCode = "would_read_differently"
)
