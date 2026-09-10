package ibkr

import (
	"bufio"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strings"
)

// An IBKR activity statement is one CSV file holding many tables. Every line
// is
//
//	<Section>,<Header|Data|SubTotal|Total>,<field>...
//
// A "Header" line names the columns of the Data lines that follow it in that
// section. A section repeats - once per currency, once per asset category -
// and re-declares its header each time, so headers are tracked per section
// name and the last one seen wins. Only Data lines carry events: SubTotal and
// Total lines restate them aggregated and are dropped.

// row is one Data line bound to the header in force for its section.
type row struct {
	section string
	col     map[string]int
	fields  []string
	line    int
}

// get returns the trimmed value of a named column. It answers "" when the
// column is absent from the section's header or when the line stops before
// it: IBKR truncates trailing empty fields.
func (r row) get(name string) string {
	i, ok := r.col[name]
	if !ok || i >= len(r.fields) {
		return ""
	}
	return strings.TrimSpace(r.fields[i])
}

// getAny returns the first of names that this section carries and fills.
// IBKR renames columns between statement flavours (Flex, Activity) and
// between locales; the caller lists the spellings it knows.
func (r row) getAny(names ...string) string {
	for _, name := range names {
		if v := r.get(name); v != "" {
			return v
		}
	}
	return ""
}

// parse reads every Data line of an activity statement, in file order.
// It tolerates a UTF-8 BOM, CRLF endings, the ragged records IBKR emits (each
// section has its own column count) and the stray quotes of free-form
// descriptions.
func parse(rd io.Reader) ([]row, error) {
	br := bufio.NewReader(rd)
	if b, err := br.Peek(3); err == nil && string(b) == "\ufeff" {
		_, _ = br.Discard(3)
	}
	cr := csv.NewReader(br)
	cr.FieldsPerRecord = -1
	cr.LazyQuotes = true
	cr.TrimLeadingSpace = true

	headers := map[string]map[string]int{}
	var rows []row
	for {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			return rows, nil
		}
		if err != nil {
			return nil, err // csv.ParseError already names the line
		}
		if len(rec) < 2 {
			continue // blank or truncated separator line
		}
		line, _ := cr.FieldPos(0)
		section, discriminator := strings.TrimSpace(rec[0]), strings.TrimSpace(rec[1])
		switch discriminator {
		case "Header":
			headers[section] = columns(rec[2:])
		case "Data":
			col, ok := headers[section]
			if !ok {
				return nil, fmt.Errorf("line %d: section %q has a Data line before its Header", line, section)
			}
			rows = append(rows, row{section: section, col: col, fields: rec[2:], line: line})
		}
	}
}

// columns indexes a header line by name; the first occurrence wins, so a
// section repeating a column name (IBKR does, for per-currency totals) keeps
// resolving to the meaningful one.
func columns(names []string) map[string]int {
	col := make(map[string]int, len(names))
	for i, name := range names {
		name = strings.TrimSpace(name)
		if _, dup := col[name]; name == "" || dup {
			continue
		}
		col[name] = i
	}
	return col
}
