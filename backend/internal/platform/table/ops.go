package table

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"regexp"
	"strconv"
	"strings"
)

// Reserved column names that operations add. Inputs may not already use them, so a later step
// can always tell the platform's annotations from customer data.
const (
	SourceFileColumn = "_source_file"
	SourceRowColumn  = "_source_row"
	DuplicateColumn  = "_duplicate"
	GroupColumn      = "_duplicate_group"
)

const (
	maxInputs     = 16
	maxAggregates = 32
	maxEncoded    = 64 << 20
)

// Stats describe what an operation did. They are shown to the model and in the run record.
type Stats map[string]int

// Named pairs a table with the workspace file it was read from.
type Named struct {
	Name  string
	Table Table
}

// Spec is the model-facing request for one operation.
type Spec struct {
	Operation string          `json:"operation"`
	Inputs    []string        `json:"inputs"`
	Output    string          `json:"output"`
	Options   json.RawMessage `json:"options,omitempty"`
}

// Output is the encoded result of Apply.
type Output struct {
	Name    string
	Data    []byte
	Columns []string
	Rows    int
	Stats   Stats
}

func columnIndexes(t Table, names []string) ([]int, error) {
	position := make(map[string]int, len(t.Columns))
	for i, column := range t.Columns {
		position[column] = i
	}
	indexes := make([]int, 0, len(names))
	var missing []string
	for _, name := range names {
		i, ok := position[name]
		if !ok {
			missing = append(missing, name)
			continue
		}
		indexes = append(indexes, i)
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("unknown column(s) %s; available columns: %s", strings.Join(quoteAll(missing), ", "), strings.Join(quoteAll(t.Columns), ", "))
	}
	return indexes, nil
}

func quoteAll(values []string) []string {
	quoted := make([]string, len(values))
	for i, value := range values {
		quoted[i] = strconv.Quote(value)
	}
	return quoted
}

// Merge stacks tables, aligning columns by name. Columns missing from a table are left empty.
func Merge(ctx context.Context, inputs []Named, addSource bool) (Table, Stats, error) {
	if len(inputs) == 0 {
		return Table{}, nil, errors.New("merge needs at least one input")
	}
	var columns []string
	index := map[string]int{}
	for _, input := range inputs {
		for _, column := range input.Table.Columns {
			if addSource && (column == SourceFileColumn || column == SourceRowColumn) {
				return Table{}, nil, fmt.Errorf("%s already has the reserved column %q; rename it or merge without source columns", input.Name, column)
			}
			if _, known := index[column]; !known {
				index[column] = len(columns)
				columns = append(columns, column)
			}
		}
	}
	if addSource {
		index[SourceFileColumn], index[SourceRowColumn] = len(columns), len(columns)+1
		columns = append(columns, SourceFileColumn, SourceRowColumn)
	}
	if len(columns) > MaxColumns {
		return Table{}, nil, fmt.Errorf("the merged table would have more than %d columns", MaxColumns)
	}
	out := Table{Columns: columns}
	for _, input := range inputs {
		mapping := make([]int, len(input.Table.Columns))
		for i, column := range input.Table.Columns {
			mapping[i] = index[column]
		}
		for number, row := range input.Table.Rows {
			if len(out.Rows) >= MaxRows {
				return Table{}, nil, fmt.Errorf("the merged table would have more than %d rows", MaxRows)
			}
			merged := make([]string, len(columns))
			for i, cell := range row {
				merged[mapping[i]] = cell
			}
			if addSource {
				merged[index[SourceFileColumn]] = input.Name
				merged[index[SourceRowColumn]] = strconv.Itoa(number + 1)
			}
			out.Rows = append(out.Rows, merged)
			if len(out.Rows)%checkEvery == 0 {
				if err := ctx.Err(); err != nil {
					return Table{}, nil, err
				}
			}
		}
	}
	return out, Stats{"inputs": len(inputs), "rows": len(out.Rows), "columns": len(columns)}, nil
}

// DedupeOptions selects what counts as the same record and what to do with repeats.
type DedupeOptions struct {
	// Keys are the columns that identify a record; empty means every column.
	Keys []string `json:"keys"`
	// Mode is "mark" (default) to annotate repeats or "remove" to drop them, keeping the first.
	Mode       string `json:"mode"`
	IgnoreCase bool   `json:"ignoreCase"`
}

// Dedupe finds repeated records. Values are compared after trimming surrounding spaces.
func Dedupe(ctx context.Context, t Table, options DedupeOptions) (Table, Stats, error) {
	if options.Mode == "" {
		options.Mode = "mark"
	}
	if options.Mode != "mark" && options.Mode != "remove" {
		return Table{}, nil, errors.New(`mode must be "mark" or "remove"`)
	}
	keyNames := options.Keys
	if len(keyNames) == 0 {
		keyNames = t.Columns
	}
	keys, err := columnIndexes(t, keyNames)
	if err != nil {
		return Table{}, nil, err
	}
	if options.Mode == "mark" {
		for _, column := range t.Columns {
			if column == DuplicateColumn || column == GroupColumn {
				return Table{}, nil, fmt.Errorf("the table already has the reserved column %q", column)
			}
		}
	}
	signature := func(row []string) string {
		var builder strings.Builder
		for _, i := range keys {
			value := strings.TrimSpace(row[i])
			if options.IgnoreCase {
				value = strings.ToLower(value)
			}
			builder.WriteString(value)
			builder.WriteByte(0x1f)
		}
		return builder.String()
	}
	first := make(map[string]int, len(t.Rows))
	counts := make(map[string]int, len(t.Rows))
	signatures := make([]string, len(t.Rows))
	for i, row := range t.Rows {
		key := signature(row)
		signatures[i] = key
		if _, seen := first[key]; !seen {
			first[key] = i
		}
		counts[key]++
		if (i+1)%checkEvery == 0 {
			if err = ctx.Err(); err != nil {
				return Table{}, nil, err
			}
		}
	}
	duplicateRows, duplicateGroups := 0, 0
	for _, count := range counts {
		if count > 1 {
			duplicateRows += count - 1
			duplicateGroups++
		}
	}
	out := Table{Columns: append([]string(nil), t.Columns...)}
	if options.Mode == "mark" {
		out.Columns = append(out.Columns, DuplicateColumn, GroupColumn)
	}
	groupIDs := map[string]int{}
	for i, row := range t.Rows {
		key := signatures[i]
		repeated := first[key] != i
		if options.Mode == "remove" {
			if !repeated {
				out.Rows = append(out.Rows, row)
			}
			continue
		}
		marked := append(append(make([]string, 0, len(row)+2), row...), strconv.FormatBool(repeated), "")
		if counts[key] > 1 {
			id, known := groupIDs[key]
			if !known {
				id = len(groupIDs) + 1
				groupIDs[key] = id
			}
			marked[len(marked)-1] = strconv.Itoa(id)
		}
		out.Rows = append(out.Rows, marked)
	}
	return out, Stats{"rowsIn": len(t.Rows), "rowsOut": len(out.Rows), "duplicateRows": duplicateRows, "duplicateGroups": duplicateGroups}, nil
}

// Aggregate is one summary column. Op is count, sum, avg, min or max; every op except count needs Column.
type Aggregate struct {
	Column string `json:"column"`
	Op     string `json:"op"`
	As     string `json:"as"`
}

// SummarizeOptions groups rows and aggregates numeric columns.
type SummarizeOptions struct {
	GroupBy    []string    `json:"groupBy"`
	Aggregates []Aggregate `json:"aggregates"`
	// SkipInvalid ignores non-numeric cells instead of failing; empty cells are always ignored.
	SkipInvalid bool `json:"skipInvalid"`
}

var decimalPattern = regexp.MustCompile(`^[+-]?([0-9]{1,30}(\.[0-9]{0,30})?|\.[0-9]{1,30})$`)

// formatRat prints at most six decimals, trimming trailing zeros.
func formatRat(value *big.Rat) string {
	text := value.FloatString(6)
	if strings.Contains(text, ".") {
		text = strings.TrimRight(strings.TrimRight(text, "0"), ".")
	}
	if text == "-0" || text == "" {
		return "0"
	}
	return text
}

type accumulator struct {
	rows     int
	count    int
	sum      *big.Rat
	min, max *big.Rat
}

// Summarize groups rows in first-seen order. Sums and averages use exact decimal arithmetic.
func Summarize(ctx context.Context, t Table, options SummarizeOptions) (Table, Stats, error) {
	if len(options.Aggregates) == 0 || len(options.Aggregates) > maxAggregates {
		// The reader of this message is usually a model that left options out; say what to send.
		return Table{}, nil, fmt.Errorf(`summarize needs options.aggregates: 1 to %d items such as {"op":"sum","column":"amount","as":"total"} (op is count, sum, avg, min or max); options.groupBy lists the columns to group by`, maxAggregates)
	}
	groupColumns, err := columnIndexes(t, options.GroupBy)
	if err != nil {
		return Table{}, nil, err
	}
	type plan struct {
		op     string
		column int
		name   string
	}
	plans := make([]plan, len(options.Aggregates))
	names := append([]string(nil), options.GroupBy...)
	for i, aggregate := range options.Aggregates {
		p := plan{op: aggregate.Op, column: -1}
		switch aggregate.Op {
		case "count":
			p.name = "count"
		case "sum", "avg", "min", "max":
			indexes, err := columnIndexes(t, []string{aggregate.Column})
			if err != nil {
				return Table{}, nil, err
			}
			p.column = indexes[0]
			p.name = aggregate.Op + "_" + aggregate.Column
		default:
			return Table{}, nil, fmt.Errorf("unknown aggregate %q; use count, sum, avg, min or max", aggregate.Op)
		}
		if aggregate.As != "" {
			p.name = aggregate.As
		}
		plans[i] = p
		names = append(names, p.name)
	}
	header, err := checkHeader(names)
	if err != nil {
		return Table{}, nil, fmt.Errorf("summary columns: %w", err)
	}
	type group struct {
		values []string
		totals []accumulator
	}
	groups := map[string]*group{}
	var order []*group
	skipped := 0
	for number, row := range t.Rows {
		var builder strings.Builder
		for _, i := range groupColumns {
			builder.WriteString(row[i])
			builder.WriteByte(0x1f)
		}
		key := builder.String()
		g, known := groups[key]
		if !known {
			g = &group{totals: make([]accumulator, len(plans))}
			for _, i := range groupColumns {
				g.values = append(g.values, row[i])
			}
			groups[key] = g
			order = append(order, g)
		}
		for i, p := range plans {
			total := &g.totals[i]
			total.rows++
			if p.op == "count" {
				continue
			}
			cell := strings.TrimSpace(row[p.column])
			if cell == "" {
				continue
			}
			if !decimalPattern.MatchString(cell) {
				if options.SkipInvalid {
					skipped++
					continue
				}
				return Table{}, nil, fmt.Errorf("row %d, column %q: %q is not a number (set skipInvalid to ignore such cells)", number+1, t.Columns[p.column], cell)
			}
			value, ok := new(big.Rat).SetString(cell)
			if !ok {
				return Table{}, nil, fmt.Errorf("row %d, column %q: %q is not a number", number+1, t.Columns[p.column], cell)
			}
			if total.sum == nil {
				total.sum, total.min, total.max = new(big.Rat), value, value
			} else {
				if value.Cmp(total.min) < 0 {
					total.min = value
				}
				if value.Cmp(total.max) > 0 {
					total.max = value
				}
			}
			total.sum.Add(total.sum, value)
			total.count++
		}
		if (number+1)%checkEvery == 0 {
			if err = ctx.Err(); err != nil {
				return Table{}, nil, err
			}
		}
	}
	out := Table{Columns: header}
	for _, g := range order {
		row := append([]string(nil), g.values...)
		for i, p := range plans {
			total := g.totals[i]
			switch {
			case p.op == "count":
				row = append(row, strconv.Itoa(total.rows))
			case total.count == 0 && p.op == "sum":
				row = append(row, "0")
			case total.count == 0:
				row = append(row, "")
			case p.op == "sum":
				row = append(row, formatRat(total.sum))
			case p.op == "avg":
				row = append(row, formatRat(new(big.Rat).Quo(total.sum, new(big.Rat).SetInt64(int64(total.count)))))
			case p.op == "min":
				row = append(row, formatRat(total.min))
			default:
				row = append(row, formatRat(total.max))
			}
		}
		out.Rows = append(out.Rows, row)
	}
	return out, Stats{"rowsIn": len(t.Rows), "groups": len(out.Rows), "skippedCells": skipped}, nil
}

// SelectOptions keeps and renames columns.
type SelectOptions struct {
	Columns []string          `json:"columns"`
	Rename  map[string]string `json:"rename"`
}

// Select projects columns in the requested order.
func Select(ctx context.Context, t Table, options SelectOptions) (Table, Stats, error) {
	if len(options.Columns) == 0 {
		return Table{}, nil, errors.New("select needs options.columns: the names of the columns to keep, in the wanted order")
	}
	indexes, err := columnIndexes(t, options.Columns)
	if err != nil {
		return Table{}, nil, err
	}
	names := make([]string, len(options.Columns))
	for i, column := range options.Columns {
		names[i] = column
		if renamed, ok := options.Rename[column]; ok {
			names[i] = renamed
		}
	}
	for from := range options.Rename {
		found := false
		for _, column := range options.Columns {
			found = found || column == from
		}
		if !found {
			return Table{}, nil, fmt.Errorf("rename refers to %q, which is not among the selected columns", from)
		}
	}
	header, err := checkHeader(names)
	if err != nil {
		return Table{}, nil, err
	}
	out := Table{Columns: header, Rows: make([][]string, 0, len(t.Rows))}
	for number, row := range t.Rows {
		picked := make([]string, len(indexes))
		for i, source := range indexes {
			picked[i] = row[source]
		}
		out.Rows = append(out.Rows, picked)
		if (number+1)%checkEvery == 0 {
			if err = ctx.Err(); err != nil {
				return Table{}, nil, err
			}
		}
	}
	return out, Stats{"rowsIn": len(t.Rows), "rowsOut": len(out.Rows), "columns": len(header)}, nil
}

func decodeOptions(raw json.RawMessage, into any) error {
	if len(bytes.TrimSpace(raw)) == 0 || string(bytes.TrimSpace(raw)) == "null" {
		return nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(into); err != nil {
		return fmt.Errorf("options: %w", err)
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return errors.New("options: unexpected data after the JSON object")
	}
	return nil
}

// Apply loads the named inputs, runs one operation and encodes the result by the output extension.
func Apply(ctx context.Context, spec Spec, load func(name string) ([]byte, error)) (Output, error) {
	outFormat, err := FormatOf(spec.Output)
	if err != nil {
		return Output{}, fmt.Errorf("output: %w", err)
	}
	if spec.Output == "" || len(spec.Output) > MaxNameBytes || strings.ContainsAny(spec.Output, "/\\\x00") {
		return Output{}, errors.New("output must be a plain file name")
	}
	if len(spec.Inputs) == 0 || len(spec.Inputs) > maxInputs {
		return Output{}, fmt.Errorf("provide between 1 and %d input files", maxInputs)
	}
	single := spec.Operation != "merge"
	if single && len(spec.Inputs) != 1 {
		return Output{}, fmt.Errorf("%s takes exactly one input; merge files first", spec.Operation)
	}
	named := make([]Named, 0, len(spec.Inputs))
	for _, name := range spec.Inputs {
		if name == spec.Output {
			return Output{}, fmt.Errorf("output %q must differ from its inputs", name)
		}
		data, err := load(name)
		if err != nil {
			return Output{}, fmt.Errorf("input %q: %w", name, err)
		}
		parsed, err := Parse(ctx, name, data)
		if err != nil {
			return Output{}, err
		}
		named = append(named, Named{Name: name, Table: parsed})
	}
	var result Table
	var stats Stats
	switch spec.Operation {
	case "merge":
		options := struct {
			AddSourceColumns *bool `json:"addSourceColumns"`
		}{}
		if err = decodeOptions(spec.Options, &options); err != nil {
			return Output{}, err
		}
		addSource := options.AddSourceColumns == nil || *options.AddSourceColumns
		result, stats, err = Merge(ctx, named, addSource)
	case "dedupe":
		var options DedupeOptions
		if err = decodeOptions(spec.Options, &options); err != nil {
			return Output{}, err
		}
		result, stats, err = Dedupe(ctx, named[0].Table, options)
	case "summarize":
		var options SummarizeOptions
		if err = decodeOptions(spec.Options, &options); err != nil {
			return Output{}, err
		}
		result, stats, err = Summarize(ctx, named[0].Table, options)
	case "select":
		var options SelectOptions
		if err = decodeOptions(spec.Options, &options); err != nil {
			return Output{}, err
		}
		result, stats, err = Select(ctx, named[0].Table, options)
	case "convert":
		if err = decodeOptions(spec.Options, &struct{}{}); err != nil {
			return Output{}, err
		}
		result, stats = named[0].Table, Stats{"rows": len(named[0].Table.Rows), "columns": len(named[0].Table.Columns)}
	default:
		return Output{}, fmt.Errorf("unknown operation %q; use merge, dedupe, summarize, select or convert", spec.Operation)
	}
	if err != nil {
		return Output{}, err
	}
	data, err := result.Encode(outFormat)
	if err != nil {
		return Output{}, err
	}
	if len(data) > maxEncoded {
		return Output{}, fmt.Errorf("the result is larger than %d bytes", maxEncoded)
	}
	return Output{Name: spec.Output, Data: data, Columns: result.Columns, Rows: len(result.Rows), Stats: stats}, nil
}
