// Package table implements the files.transform tool: deterministic operations on delimited and
// JSON tables. It is trusted code that processes untrusted data, so every dimension is bounded
// and nothing here executes data. Free-form analysis belongs to the sandbox, not to this package.
package table

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Bounds keep one transform from exhausting a worker.
const (
	MaxRows      = 500_000
	MaxColumns   = 512
	MaxCellBytes = 1 << 20
	MaxNameBytes = 200
	checkEvery   = 8192
)

// Format is a supported table encoding, chosen by file extension.
type Format string

const (
	CSV  Format = "csv"
	TSV  Format = "tsv"
	JSON Format = "json"
)

// Table is a header and string cells. Every row has exactly len(Columns) cells.
type Table struct {
	Columns []string
	Rows    [][]string
}

// FormatOf maps a file name to its format; only declared formats are accepted.
func FormatOf(name string) (Format, error) {
	switch strings.ToLower(path.Ext(name)) {
	case ".csv":
		return CSV, nil
	case ".tsv":
		return TSV, nil
	case ".json":
		return JSON, nil
	}
	return "", fmt.Errorf("%q is not a supported table file; use .csv, .tsv or .json", name)
}

// Parse decodes a file according to its extension.
func Parse(ctx context.Context, name string, data []byte) (Table, error) {
	format, err := FormatOf(name)
	if err != nil {
		return Table{}, err
	}
	table, err := parse(ctx, format, data)
	if err != nil {
		return Table{}, fmt.Errorf("%s: %w", name, err)
	}
	return table, nil
}

func parse(ctx context.Context, format Format, data []byte) (Table, error) {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	if !utf8.Valid(data) {
		return Table{}, errors.New("the file is not valid UTF-8")
	}
	switch format {
	case CSV:
		return parseDelimited(ctx, data, ',')
	case TSV:
		return parseDelimited(ctx, data, '\t')
	default:
		return parseJSON(ctx, data)
	}
}

func checkHeader(columns []string) ([]string, error) {
	if len(columns) == 0 || len(columns) > MaxColumns {
		return nil, fmt.Errorf("the table needs between 1 and %d columns", MaxColumns)
	}
	seen := map[string]bool{}
	cleaned := make([]string, len(columns))
	for i, column := range columns {
		column = strings.TrimSpace(column)
		switch {
		case column == "":
			return nil, fmt.Errorf("column %d has an empty name", i+1)
		case len(column) > MaxNameBytes:
			return nil, fmt.Errorf("column %d has a name longer than %d bytes", i+1, MaxNameBytes)
		case seen[column]:
			return nil, fmt.Errorf("column name %q appears more than once", column)
		}
		seen[column] = true
		cleaned[i] = column
	}
	return cleaned, nil
}

func parseDelimited(ctx context.Context, data []byte, comma rune) (Table, error) {
	reader := csv.NewReader(bytes.NewReader(data))
	reader.Comma = comma
	reader.FieldsPerRecord = -1
	header, err := reader.Read()
	if errors.Is(err, io.EOF) {
		return Table{}, errors.New("the file is empty")
	}
	if err != nil {
		return Table{}, err
	}
	columns, err := checkHeader(header)
	if err != nil {
		return Table{}, err
	}
	table := Table{Columns: columns}
	for {
		record, err := reader.Read()
		if errors.Is(err, io.EOF) {
			return table, nil
		}
		if err != nil {
			return Table{}, err
		}
		if len(table.Rows) >= MaxRows {
			return Table{}, fmt.Errorf("the table has more than %d rows", MaxRows)
		}
		if len(record) > len(columns) {
			return Table{}, fmt.Errorf("row %d has %d fields but the header has %d", len(table.Rows)+1, len(record), len(columns))
		}
		for len(record) < len(columns) {
			record = append(record, "")
		}
		for _, cell := range record {
			if len(cell) > MaxCellBytes {
				return Table{}, fmt.Errorf("row %d has a cell larger than %d bytes", len(table.Rows)+1, MaxCellBytes)
			}
		}
		table.Rows = append(table.Rows, record)
		if len(table.Rows)%checkEvery == 0 {
			if err = ctx.Err(); err != nil {
				return Table{}, err
			}
		}
	}
}

// scalar renders a JSON scalar as a cell; nested values are refused rather than flattened.
func scalar(raw json.RawMessage) (string, error) {
	text := strings.TrimSpace(string(raw))
	switch {
	case strings.HasPrefix(text, `"`):
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return "", err
		}
		return value, nil
	case text == "null":
		return "", nil
	case strings.HasPrefix(text, "{"), strings.HasPrefix(text, "["):
		return "", errors.New("nested objects and arrays are not supported; flatten the data first")
	default:
		return text, nil
	}
}

func parseJSON(ctx context.Context, data []byte) (Table, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	opening, err := decoder.Token()
	if delim, ok := opening.(json.Delim); err != nil || !ok || delim != '[' {
		return Table{}, errors.New("expected a JSON array of objects")
	}
	var columns []string
	index := map[string]int{}
	var rows [][]string
	for decoder.More() {
		if len(rows) >= MaxRows {
			return Table{}, fmt.Errorf("the table has more than %d rows", MaxRows)
		}
		start, err := decoder.Token()
		if delim, ok := start.(json.Delim); err != nil || !ok || delim != '{' {
			return Table{}, fmt.Errorf("row %d is not a JSON object", len(rows)+1)
		}
		cells := map[int]string{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			key, ok := keyToken.(string)
			if err != nil || !ok {
				return Table{}, fmt.Errorf("row %d has an invalid key", len(rows)+1)
			}
			var raw json.RawMessage
			if err = decoder.Decode(&raw); err != nil {
				return Table{}, fmt.Errorf("row %d: %w", len(rows)+1, err)
			}
			value, err := scalar(raw)
			if err != nil {
				return Table{}, fmt.Errorf("row %d, key %q: %w", len(rows)+1, key, err)
			}
			if len(value) > MaxCellBytes {
				return Table{}, fmt.Errorf("row %d has a cell larger than %d bytes", len(rows)+1, MaxCellBytes)
			}
			position, known := index[key]
			if !known {
				if len(columns) >= MaxColumns {
					return Table{}, fmt.Errorf("the table has more than %d columns", MaxColumns)
				}
				position = len(columns)
				columns = append(columns, key)
				index[key] = position
			}
			cells[position] = value
		}
		if _, err = decoder.Token(); err != nil {
			return Table{}, err
		}
		row := make([]string, len(columns))
		for position, value := range cells {
			row[position] = value
		}
		rows = append(rows, row)
		if len(rows)%checkEvery == 0 {
			if err = ctx.Err(); err != nil {
				return Table{}, err
			}
		}
	}
	if _, err = decoder.Token(); err != nil {
		return Table{}, err
	}
	cleaned, err := checkHeader(columns)
	if err != nil {
		return Table{}, err
	}
	for i, row := range rows {
		for len(row) < len(cleaned) {
			row = append(row, "")
		}
		rows[i] = row
	}
	return Table{Columns: cleaned, Rows: rows}, nil
}

var numberLiteral = regexp.MustCompile(`^-?(0|[1-9][0-9]{0,14})(\.[0-9]{1,15})?$`)

func jsonString(value string) []byte {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(value)
	return bytes.TrimRight(buffer.Bytes(), "\n")
}

// Encode renders the table. JSON keeps plain numeric cells as numbers and everything else as strings.
func (t Table) Encode(format Format) ([]byte, error) {
	var buffer bytes.Buffer
	switch format {
	case CSV, TSV:
		writer := csv.NewWriter(&buffer)
		if format == TSV {
			writer.Comma = '\t'
		}
		if err := writer.Write(t.Columns); err != nil {
			return nil, err
		}
		for _, row := range t.Rows {
			if err := writer.Write(row); err != nil {
				return nil, err
			}
		}
		writer.Flush()
		if err := writer.Error(); err != nil {
			return nil, err
		}
	case JSON:
		names := make([][]byte, len(t.Columns))
		for i, column := range t.Columns {
			names[i] = jsonString(column)
		}
		buffer.WriteString("[")
		for r, row := range t.Rows {
			if r > 0 {
				buffer.WriteString(",")
			}
			buffer.WriteString("\n  {")
			for c, cell := range row {
				if c > 0 {
					buffer.WriteString(", ")
				}
				buffer.Write(names[c])
				buffer.WriteString(": ")
				if numberLiteral.MatchString(cell) {
					buffer.WriteString(cell)
				} else {
					buffer.Write(jsonString(cell))
				}
			}
			buffer.WriteString("}")
		}
		buffer.WriteString("\n]\n")
	default:
		return nil, fmt.Errorf("unsupported output format %q", format)
	}
	return buffer.Bytes(), nil
}
