package table

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"reflect"
	"strings"
	"testing"
)

func newRat(text string) (*big.Rat, bool) { return new(big.Rat).SetString(text) }

const (
	ordersA = "order_id,region,amount\nA-1,east,10.50\nA-2,west,20\nA-3,east,5.25\n"
	ordersB = "order_id,region,amount,note\nA-2,west,20,dup\nB-1,north,7\n"
)

func loader(files map[string]string) func(string) ([]byte, error) {
	return func(name string) ([]byte, error) {
		content, ok := files[name]
		if !ok {
			return nil, errors.New("no such file")
		}
		return []byte(content), nil
	}
}

func mergedOrders(t *testing.T) Table {
	t.Helper()
	merged, _, err := Merge(context.Background(), []Named{{"orders_a.csv", mustParse(t, "a.csv", ordersA)}, {"orders_b.csv", mustParse(t, "b.csv", ordersB)}}, true)
	if err != nil {
		t.Fatal(err)
	}
	return merged
}

func TestMergeAlignsColumnsAndTracksSources(t *testing.T) {
	merged := mergedOrders(t)
	wantColumns := []string{"order_id", "region", "amount", "note", SourceFileColumn, SourceRowColumn}
	if !reflect.DeepEqual(merged.Columns, wantColumns) {
		t.Fatalf("columns = %q", merged.Columns)
	}
	if len(merged.Rows) != 5 {
		t.Fatalf("rows = %d", len(merged.Rows))
	}
	if !reflect.DeepEqual(merged.Rows[0], []string{"A-1", "east", "10.50", "", "orders_a.csv", "1"}) || !reflect.DeepEqual(merged.Rows[4], []string{"B-1", "north", "7", "", "orders_b.csv", "2"}) {
		t.Fatalf("rows = %q", merged.Rows)
	}
	plain, stats, err := Merge(context.Background(), []Named{{"a.csv", mustParse(t, "a.csv", ordersA)}}, false)
	if err != nil || len(plain.Columns) != 3 || stats["rows"] != 3 || stats["inputs"] != 1 {
		t.Fatalf("plain merge = %+v %v %v", plain, stats, err)
	}
	reserved := mustParse(t, "r.csv", SourceFileColumn+",x\n1,2\n")
	if _, _, err = Merge(context.Background(), []Named{{"r.csv", reserved}}, true); err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("reserved column = %v", err)
	}
	if _, _, err = Merge(context.Background(), nil, true); err == nil {
		t.Fatal("empty merge accepted")
	}
}

func TestMergeHonorsCancellation(t *testing.T) {
	big := Table{Columns: []string{"a"}}
	for i := 0; i < checkEvery+1; i++ {
		big.Rows = append(big.Rows, []string{"1"})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := Merge(ctx, []Named{{"big.csv", big}}, false); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
}

func TestDedupeMarksRepeatsAndKeepsTheFirst(t *testing.T) {
	marked, stats, err := Dedupe(context.Background(), mergedOrders(t), DedupeOptions{Keys: []string{"order_id"}})
	if err != nil {
		t.Fatal(err)
	}
	if stats["duplicateRows"] != 1 || stats["duplicateGroups"] != 1 || stats["rowsOut"] != 5 {
		t.Fatalf("stats = %v", stats)
	}
	n := len(marked.Columns)
	if marked.Columns[n-2] != DuplicateColumn || marked.Columns[n-1] != GroupColumn {
		t.Fatalf("columns = %q", marked.Columns)
	}
	// Row order is preserved: A-1, A-2 (first), A-3, A-2 (repeat), B-1.
	got := make([]string, 0, len(marked.Rows))
	for _, row := range marked.Rows {
		got = append(got, fmt.Sprintf("%s:%s:%s", row[0], row[n-2], row[n-1]))
	}
	want := []string{"A-1:false:", "A-2:false:1", "A-3:false:", "A-2:true:1", "B-1:false:"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("marks = %q", got)
	}
	removed, stats, err := Dedupe(context.Background(), mergedOrders(t), DedupeOptions{Keys: []string{"order_id"}, Mode: "remove"})
	if err != nil || len(removed.Rows) != 4 || len(removed.Columns) != 6 || stats["rowsOut"] != 4 {
		t.Fatalf("remove = %d rows, %v, %v", len(removed.Rows), stats, err)
	}
}

func TestDedupeNormalizesAndValidates(t *testing.T) {
	people := mustParse(t, "p.csv", "name,city\nAda , Paris\nada,paris\nBob,Rome\n")
	exact, stats, err := Dedupe(context.Background(), people, DedupeOptions{Keys: []string{"name"}, Mode: "remove"})
	if err != nil || len(exact.Rows) != 3 || stats["duplicateRows"] != 0 {
		t.Fatalf("case-sensitive = %d rows %v %v", len(exact.Rows), stats, err)
	}
	folded, stats, err := Dedupe(context.Background(), people, DedupeOptions{Keys: []string{"name", "city"}, Mode: "remove", IgnoreCase: true})
	if err != nil || len(folded.Rows) != 2 || stats["duplicateRows"] != 1 {
		t.Fatalf("ignore case = %d rows %v %v", len(folded.Rows), stats, err)
	}
	all, _, err := Dedupe(context.Background(), mustParse(t, "d.csv", "a,b\n1,2\n1,2\n3,4\n"), DedupeOptions{Mode: "remove"})
	if err != nil || len(all.Rows) != 2 {
		t.Fatalf("all columns = %d rows %v", len(all.Rows), err)
	}
	if _, _, err = Dedupe(context.Background(), people, DedupeOptions{Keys: []string{"missing"}}); err == nil || !strings.Contains(err.Error(), `"missing"`) || !strings.Contains(err.Error(), `"name"`) {
		t.Fatalf("unknown key = %v", err)
	}
	if _, _, err = Dedupe(context.Background(), people, DedupeOptions{Mode: "bogus"}); err == nil {
		t.Fatal("unknown mode accepted")
	}
	annotated := mustParse(t, "n.csv", DuplicateColumn+"\ntrue\n")
	if _, _, err = Dedupe(context.Background(), annotated, DedupeOptions{}); err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("reserved column = %v", err)
	}
}

func TestSummarizeUsesExactDecimalArithmetic(t *testing.T) {
	summary, stats, err := Summarize(context.Background(), mergedOrders(t), SummarizeOptions{
		GroupBy: []string{"region"},
		Aggregates: []Aggregate{
			{Op: "count"},
			{Column: "amount", Op: "sum"},
			{Column: "amount", Op: "avg", As: "mean"},
			{Column: "amount", Op: "min"},
			{Column: "amount", Op: "max"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	wantColumns := []string{"region", "count", "sum_amount", "mean", "min_amount", "max_amount"}
	if !reflect.DeepEqual(summary.Columns, wantColumns) {
		t.Fatalf("columns = %q", summary.Columns)
	}
	want := [][]string{
		{"east", "2", "15.75", "7.875", "5.25", "10.5"},
		{"west", "2", "40", "20", "20", "20"},
		{"north", "1", "7", "7", "7", "7"},
	}
	if !reflect.DeepEqual(summary.Rows, want) {
		t.Fatalf("rows = %q", summary.Rows)
	}
	if stats["groups"] != 3 || stats["rowsIn"] != 5 {
		t.Fatalf("stats = %v", stats)
	}
	floats := mustParse(t, "f.csv", "x\n0.1\n0.2\n")
	total, _, err := Summarize(context.Background(), floats, SummarizeOptions{Aggregates: []Aggregate{{Column: "x", Op: "sum"}}})
	if err != nil || total.Rows[0][0] != "0.3" {
		t.Fatalf("0.1+0.2 = %v %v", total.Rows, err)
	}
}

func TestSummarizeHandlesGapsAndBadNumbers(t *testing.T) {
	data := mustParse(t, "d.csv", "k,v\na,1\na,\na,oops\nb,\n")
	if _, _, err := Summarize(context.Background(), data, SummarizeOptions{GroupBy: []string{"k"}, Aggregates: []Aggregate{{Column: "v", Op: "sum"}}}); err == nil || !strings.Contains(err.Error(), "row 3") || !strings.Contains(err.Error(), "skipInvalid") {
		t.Fatalf("bad number = %v", err)
	}
	summary, stats, err := Summarize(context.Background(), data, SummarizeOptions{GroupBy: []string{"k"}, SkipInvalid: true, Aggregates: []Aggregate{{Column: "v", Op: "sum"}, {Column: "v", Op: "avg"}, {Column: "v", Op: "min"}}})
	if err != nil {
		t.Fatal(err)
	}
	// Group b has no numeric cells: sum is 0, but avg and min are unknown rather than zero.
	want := [][]string{{"a", "1", "1", "1"}, {"b", "0", "", ""}}
	if !reflect.DeepEqual(summary.Rows, want) || stats["skippedCells"] != 3 {
		t.Fatalf("rows = %q stats = %v", summary.Rows, stats)
	}
	for name, options := range map[string]SummarizeOptions{
		"no aggregates":     {},
		"unknown op":        {Aggregates: []Aggregate{{Column: "v", Op: "median"}}},
		"missing column":    {Aggregates: []Aggregate{{Column: "nope", Op: "sum"}}},
		"unknown group":     {GroupBy: []string{"nope"}, Aggregates: []Aggregate{{Op: "count"}}},
		"duplicate outputs": {Aggregates: []Aggregate{{Op: "count"}, {Op: "count"}}},
		"output shadows":    {GroupBy: []string{"k"}, Aggregates: []Aggregate{{Op: "count", As: "k"}}},
	} {
		if _, _, err = Summarize(context.Background(), data, options); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// A model that sends no options needs to be told what to send, or it repeats the same call.
	if _, _, err = Summarize(context.Background(), data, SummarizeOptions{}); err == nil || !strings.Contains(err.Error(), "options.aggregates") || !strings.Contains(err.Error(), `"op":"sum"`) {
		t.Errorf("an empty summarize says %v", err)
	}
}

func TestSelectProjectsAndRenames(t *testing.T) {
	selected, _, err := Select(context.Background(), mustParse(t, "a.csv", ordersA), SelectOptions{Columns: []string{"amount", "order_id"}, Rename: map[string]string{"amount": "total"}})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(selected.Columns, []string{"total", "order_id"}) || !reflect.DeepEqual(selected.Rows[1], []string{"20", "A-2"}) {
		t.Fatalf("selected = %+v", selected)
	}
	source := mustParse(t, "a.csv", ordersA)
	for name, options := range map[string]SelectOptions{
		"no columns":        {},
		"unknown column":    {Columns: []string{"nope"}},
		"rename unselected": {Columns: []string{"amount"}, Rename: map[string]string{"region": "r"}},
		"rename collision":  {Columns: []string{"amount", "region"}, Rename: map[string]string{"amount": "region"}},
		"rename to empty":   {Columns: []string{"amount"}, Rename: map[string]string{"amount": " "}},
	} {
		if _, _, err = Select(context.Background(), source, options); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, _, err = Select(context.Background(), source, SelectOptions{}); err == nil || !strings.Contains(err.Error(), "options.columns") {
		t.Errorf("an empty select says %v", err)
	}
}

func TestApplyRunsOperationsEndToEnd(t *testing.T) {
	files := map[string]string{"orders_a.csv": ordersA, "orders_b.csv": ordersB}
	merged, err := Apply(context.Background(), Spec{Operation: "merge", Inputs: []string{"orders_a.csv", "orders_b.csv"}, Output: "all.csv"}, loader(files))
	if err != nil {
		t.Fatal(err)
	}
	if merged.Name != "all.csv" || merged.Rows != 5 || merged.Stats["inputs"] != 2 || !strings.HasPrefix(string(merged.Data), "order_id,region,amount,note,_source_file,_source_row\n") {
		t.Fatalf("merged = %q %d %v", merged.Data, merged.Rows, merged.Stats)
	}
	files["all.csv"] = string(merged.Data)
	marked, err := Apply(context.Background(), Spec{Operation: "dedupe", Inputs: []string{"all.csv"}, Output: "marked.csv", Options: []byte(`{"keys":["order_id"]}`)}, loader(files))
	if err != nil || marked.Stats["duplicateRows"] != 1 {
		t.Fatalf("dedupe = %+v %v", marked.Stats, err)
	}
	converted, err := Apply(context.Background(), Spec{Operation: "convert", Inputs: []string{"orders_a.csv"}, Output: "orders.json", Options: []byte("null")}, loader(files))
	if err != nil || !strings.Contains(string(converted.Data), `"amount": 10.50`) {
		t.Fatalf("convert = %q %v", converted.Data, err)
	}
	summary, err := Apply(context.Background(), Spec{Operation: "summarize", Inputs: []string{"orders_a.csv"}, Output: "s.tsv", Options: []byte(`{"groupBy":["region"],"aggregates":[{"op":"count"}]}`)}, loader(files))
	if err != nil || string(summary.Data) != "region\tcount\neast\t2\nwest\t1\n" {
		t.Fatalf("summarize = %q %v", summary.Data, err)
	}
	picked, err := Apply(context.Background(), Spec{Operation: "select", Inputs: []string{"orders_a.csv"}, Output: "p.csv", Options: []byte(`{"columns":["order_id"]}`)}, loader(files))
	if err != nil || string(picked.Data) != "order_id\nA-1\nA-2\nA-3\n" {
		t.Fatalf("select = %q %v", picked.Data, err)
	}
}

func TestApplyRejectsBadSpecs(t *testing.T) {
	files := map[string]string{"a.csv": ordersA, "b.csv": ordersB, "bad.csv": "a,a\n1,2\n"}
	cases := map[string]Spec{
		"no output":             {Operation: "convert", Inputs: []string{"a.csv"}},
		"output extension":      {Operation: "convert", Inputs: []string{"a.csv"}, Output: "out.xlsx"},
		"output path":           {Operation: "convert", Inputs: []string{"a.csv"}, Output: "dir/out.csv"},
		"output is input":       {Operation: "convert", Inputs: []string{"a.csv"}, Output: "a.csv"},
		"no inputs":             {Operation: "merge", Output: "out.csv"},
		"too many inputs":       {Operation: "merge", Inputs: make([]string, maxInputs+1), Output: "out.csv"},
		"two inputs for dedupe": {Operation: "dedupe", Inputs: []string{"a.csv", "b.csv"}, Output: "out.csv"},
		"unknown operation":     {Operation: "pivot", Inputs: []string{"a.csv"}, Output: "out.csv"},
		"missing input":         {Operation: "convert", Inputs: []string{"gone.csv"}, Output: "out.csv"},
		"unparseable input":     {Operation: "convert", Inputs: []string{"bad.csv"}, Output: "out.csv"},
		"unknown option":        {Operation: "dedupe", Inputs: []string{"a.csv"}, Output: "out.csv", Options: []byte(`{"keys":["order_id"],"colour":"red"}`)},
		"option type":           {Operation: "dedupe", Inputs: []string{"a.csv"}, Output: "out.csv", Options: []byte(`{"keys":"order_id"}`)},
		"trailing data":         {Operation: "dedupe", Inputs: []string{"a.csv"}, Output: "out.csv", Options: []byte(`{} {}`)},
		"convert options":       {Operation: "convert", Inputs: []string{"a.csv"}, Output: "out.csv", Options: []byte(`{"to":"json"}`)},
	}
	for name, spec := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Apply(context.Background(), spec, loader(files)); err == nil {
				t.Fatal("accepted")
			}
		})
	}
}

func TestFormatRat(t *testing.T) {
	for in, want := range map[string]string{"0": "0", "-0.0000001": "0", "1.500000": "1.5", "100": "100", "-2.25": "-2.25", "0.0000005": "0.000001"} {
		value, ok := newRat(in)
		if !ok {
			t.Fatalf("cannot parse %s", in)
		}
		if got := formatRat(value); got != want {
			t.Errorf("formatRat(%s) = %s, want %s", in, got, want)
		}
	}
}
