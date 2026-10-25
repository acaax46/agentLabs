package table

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func mustParse(t *testing.T, name, content string) Table {
	t.Helper()
	parsed, err := Parse(context.Background(), name, []byte(content))
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	return parsed
}

func TestParseCSVHandlesBOMQuotesAndRaggedRows(t *testing.T) {
	parsed := mustParse(t, "a.csv", "\xef\xbb\xbforder_id, note ,amount\r\nA-1,\"hello, \"\"world\"\"\",10\r\nA-2,short\r\n\r\nA-3,,3\r\n")
	if !reflect.DeepEqual(parsed.Columns, []string{"order_id", "note", "amount"}) {
		t.Fatalf("columns = %q", parsed.Columns)
	}
	want := [][]string{{"A-1", `hello, "world"`, "10"}, {"A-2", "short", ""}, {"A-3", "", "3"}}
	if !reflect.DeepEqual(parsed.Rows, want) {
		t.Fatalf("rows = %q", parsed.Rows)
	}
}

func TestParseRejectsMalformedTables(t *testing.T) {
	cases := map[string]struct{ name, content, want string }{
		"empty":            {"a.csv", "", "empty"},
		"too wide":         {"a.csv", "a,b\n1,2,3\n", "row 1 has 3 fields"},
		"duplicate header": {"a.csv", "a,a\n1,2\n", "more than once"},
		"empty header":     {"a.csv", "a,\n1,2\n", "empty name"},
		"bad utf8":         {"a.csv", "a\n\xff\n", "UTF-8"},
		"bad quote":        {"a.csv", "a,b\n\"1,2\n", "quote"},
		"wrong extension":  {"a.xlsx", "a", "not a supported table file"},
		"json not array":   {"a.json", `{"a":1}`, "array of objects"},
		"json row scalar":  {"a.json", `[1]`, "not a JSON object"},
		"json nested":      {"a.json", `[{"a":{"b":1}}]`, "nested"},
		"json empty key":   {"a.json", `[{"":1}]`, "empty name"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Parse(context.Background(), c.name, []byte(c.content))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("error = %v, want it to mention %q", err, c.want)
			}
		})
	}
}

func TestParseEnforcesRowAndColumnBounds(t *testing.T) {
	var wide strings.Builder
	for i := 0; i <= MaxColumns; i++ {
		if i > 0 {
			wide.WriteString(",")
		}
		wide.WriteString("c" + strings.Repeat("x", 1) + string(rune('a'+i%26)) + strings.Repeat("y", i/26))
	}
	if _, err := Parse(context.Background(), "wide.csv", []byte(wide.String()+"\n")); err == nil || !strings.Contains(err.Error(), "between 1 and") {
		t.Fatalf("too many columns = %v", err)
	}
	if _, err := Parse(context.Background(), "big.csv", []byte("a\n"+strings.Repeat("x", MaxCellBytes+1)+"\n")); err == nil || !strings.Contains(err.Error(), "cell larger") {
		t.Fatalf("oversized cell = %v", err)
	}
}

func TestParseTSVAndJSONPreserveKeyOrderAndPadRows(t *testing.T) {
	tsv := mustParse(t, "a.tsv", "id\tname\n1\tAda Lovelace\n")
	if !reflect.DeepEqual(tsv.Rows, [][]string{{"1", "Ada Lovelace"}}) {
		t.Fatalf("tsv rows = %q", tsv.Rows)
	}
	parsed := mustParse(t, "a.json", `[{"b":1,"a":"x"},{"c":true,"b":2.50,"a":null}]`)
	if !reflect.DeepEqual(parsed.Columns, []string{"b", "a", "c"}) {
		t.Fatalf("columns = %q", parsed.Columns)
	}
	want := [][]string{{"1", "x", ""}, {"2.50", "", "true"}}
	if !reflect.DeepEqual(parsed.Rows, want) {
		t.Fatalf("rows = %q", parsed.Rows)
	}
}

func TestEncodeRoundTripsAndKeepsPlainNumbersAsNumbers(t *testing.T) {
	original := Table{Columns: []string{"zip", "n", "text", "flag"}, Rows: [][]string{{"007", "12.5", `say "hi", <b>`, "true"}, {"", "-3", "1234567890123456", "x"}}}
	csvBytes, err := original.Encode(CSV)
	if err != nil {
		t.Fatal(err)
	}
	if got := mustParse(t, "x.csv", string(csvBytes)); !reflect.DeepEqual(got, original) {
		t.Fatalf("csv round trip = %+v", got)
	}
	tsvBytes, err := original.Encode(TSV)
	if err != nil {
		t.Fatal(err)
	}
	if got := mustParse(t, "x.tsv", string(tsvBytes)); !reflect.DeepEqual(got, original) {
		t.Fatalf("tsv round trip = %+v", got)
	}
	jsonBytes, err := original.Encode(JSON)
	if err != nil {
		t.Fatal(err)
	}
	text := string(jsonBytes)
	for _, fragment := range []string{`"zip": "007"`, `"n": 12.5`, `"text": "say \"hi\", <b>"`, `"flag": "true"`, `"n": -3`, `"text": "1234567890123456"`} {
		if !strings.Contains(text, fragment) {
			t.Fatalf("json output lacks %s:\n%s", fragment, text)
		}
	}
	if got := mustParse(t, "x.json", text); got.Columns[0] != "zip" || len(got.Rows) != 2 {
		t.Fatalf("json round trip = %+v", got)
	}
	if _, err = original.Encode("xml"); err == nil {
		t.Fatal("unsupported format accepted")
	}
}

func TestParseHonorsCancellation(t *testing.T) {
	var content strings.Builder
	content.WriteString("a\n")
	for i := 0; i < checkEvery+1; i++ {
		content.WriteString("1\n")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Parse(ctx, "a.csv", []byte(content.String())); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
}
