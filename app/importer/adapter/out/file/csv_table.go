package file

import (
	"bufio"
	"bytes"
	"encoding/csv"
	"errors"
	"io"
	"slices"
	"strconv"
	"strings"

	"github.com/maxjkfc/maxos/app/importer/usecase/port/out/filefailure"
)

// utf8BOM is the byte order mark that some programs put at the start of a UTF-8 file.
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// csvTable reads a CSV file whose first record is a header. It finds the columns by
// the names in the header, so that their order in the file does not matter, and it
// knows the line each record begins on, which is not its number among the records
// when a field has several lines.
type csvTable struct {
	rd   *csv.Reader
	cols map[string]int
	size int // how many columns the header has: every record has as many
}

// csvRecord is a record of a csvTable.
type csvRecord struct {
	// line is the line of the file the record begins on, from 1.
	line   int
	fields []string
	cols   map[string]int
}

// openCSV reads the header of r, a UTF-8 file with or without a byte order mark,
// and checks that it has every column of required. Lines that begin with comment
// are left out, unless it is zero. A file with no header is a failure, and so is a
// header that has a column twice or lacks one that is required.
func openCSV(r io.Reader, comment rune, required ...string) (*csvTable, error) {
	br := bufio.NewReader(r)
	if head, err := br.Peek(len(utf8BOM)); err == nil && bytes.Equal(head, utf8BOM) {
		_, _ = br.Discard(len(utf8BOM))
	}
	rd := csv.NewReader(br)
	// The fields of every record are counted against the header in next, which can
	// say on what line a record is short.
	rd.FieldsPerRecord = -1
	rd.Comment = comment

	header, err := rd.Read()
	if errors.Is(err, io.EOF) {
		return nil, failAt(filefailure.Empty, 0)
	}
	if err != nil {
		return nil, csvFailure(err)
	}
	line, _ := rd.FieldPos(0)

	cols := make(map[string]int, len(required))
	for i, name := range header {
		name = strings.TrimSpace(name)
		if !slices.Contains(required, name) {
			continue // a column that is not used
		}
		if _, twice := cols[name]; twice {
			return nil, failAt(filefailure.ColumnTwice, line, name)
		}
		cols[name] = i
	}
	var missing []string
	for _, name := range required {
		if _, ok := cols[name]; !ok {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return nil, failAt(filefailure.ColumnsMissing, line, missing...)
	}
	return &csvTable{rd: rd, cols: cols, size: len(header)}, nil
}

// next returns the next record, or io.EOF after the last. A record that has not as
// many fields as the header is a failure.
func (t *csvTable) next() (csvRecord, error) {
	fields, err := t.rd.Read()
	if errors.Is(err, io.EOF) {
		return csvRecord{}, io.EOF
	}
	if err != nil {
		return csvRecord{}, csvFailure(err)
	}
	line, _ := t.rd.FieldPos(0)
	if len(fields) != t.size {
		return csvRecord{}, failAt(filefailure.FieldCount, line, strconv.Itoa(len(fields)), strconv.Itoa(t.size))
	}
	return csvRecord{line: line, fields: fields, cols: t.cols}, nil
}

// text returns the field of the column without the whitespace around it, and with
// the line endings inside it as "\n".
func (r csvRecord) text(col string) string {
	return strings.TrimSpace(strings.ReplaceAll(r.fields[r.cols[col]], "\r\n", "\n"))
}
