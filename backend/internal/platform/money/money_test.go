package money

import (
	"encoding/json"
	"math"
	"testing"
)

func TestMicrosJSONUsesDecimalStrings(t *testing.T) {
	raw, err := json.Marshal(struct {
		Amount Micros  `json:"amount"`
		Unset  *Micros `json:"unset"`
	}{Amount: 9007199254740993})
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"amount":"9007199254740993","unset":null}` {
		t.Fatalf("encoded amount = %s", raw)
	}
	var decoded struct {
		Amount Micros  `json:"amount"`
		Unset  *Micros `json:"unset"`
	}
	if err = json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Amount != 9007199254740993 || decoded.Unset != nil {
		t.Fatalf("decoded = %+v", decoded)
	}
	for _, bad := range []string{`{"amount":12}`, `{"amount":"1.5"}`, `{"amount":"abc"}`, `{"amount":""}`} {
		if err = json.Unmarshal([]byte(bad), &decoded); err == nil {
			t.Fatalf("accepted %s", bad)
		}
	}
	// JSON null leaves a plain value untouched, matching encoding/json conventions.
	decoded.Amount = 7
	if err = json.Unmarshal([]byte(`{"amount":null}`), &decoded); err != nil || decoded.Amount != 7 {
		t.Fatalf("null amount = %d, %v", decoded.Amount, err)
	}
}

func TestAddAndSumRejectOverflow(t *testing.T) {
	if _, err := Add(math.MaxInt64, 1); err != ErrOverflow {
		t.Fatalf("positive overflow = %v", err)
	}
	if _, err := Add(math.MinInt64, -1); err != ErrOverflow {
		t.Fatalf("negative overflow = %v", err)
	}
	total, err := Sum(1, 2, 3)
	if err != nil || total != 6 {
		t.Fatalf("sum = %d, %v", total, err)
	}
	if _, err = Sum(math.MaxInt64, 1); err != ErrOverflow {
		t.Fatalf("sum overflow = %v", err)
	}
}

func TestMulDivCeilRoundsPartialUnitsUp(t *testing.T) {
	cases := []struct {
		quantity int64
		rate     Micros
		divisor  int64
		want     Micros
	}{
		{0, 10, 1, 0},
		{1, 1, 1_000_000, 1},
		{1_000_000, 3, 1_000_000, 3},
		{1_000_001, 3, 1_000_000, 4},
		{2, 5, 4, 3},
	}
	for _, c := range cases {
		got, err := MulDivCeil(c.quantity, c.rate, c.divisor)
		if err != nil || got != c.want {
			t.Fatalf("MulDivCeil(%d,%d,%d) = %d, %v; want %d", c.quantity, c.rate, c.divisor, got, err, c.want)
		}
	}
	if _, err := MulDivCeil(-1, 1, 1); err != ErrNegative {
		t.Fatalf("negative quantity = %v", err)
	}
	if _, err := MulDivCeil(1, -1, 1); err != ErrNegative {
		t.Fatalf("negative rate = %v", err)
	}
	if _, err := MulDivCeil(1, 1, 0); err == nil {
		t.Fatal("zero divisor accepted")
	}
	if _, err := MulDivCeil(math.MaxInt64, math.MaxInt64, 1); err != ErrOverflow {
		t.Fatalf("product overflow = %v", err)
	}
}

func TestSumProductCeilRoundsOnce(t *testing.T) {
	// 1 + 1 + 1 millionths each round up to 1 separately (3), but together they are 0.000003 -> 1.
	got, err := SumProductCeil([]int64{1, 1, 1}, []Micros{1, 1, 1}, 1_000_000)
	if err != nil || got != 1 {
		t.Fatalf("sum = %d, %v", got, err)
	}
	got, err = SumProductCeil([]int64{1_000_000, 500_000}, []Micros{2, 4}, 1_000_000)
	if err != nil || got != 4 {
		t.Fatalf("sum = %d, %v", got, err)
	}
	if got, err = SumProductCeil(nil, nil, 1); err != nil || got != 0 {
		t.Fatalf("empty = %d, %v", got, err)
	}
	if _, err = SumProductCeil([]int64{1}, nil, 1); err == nil {
		t.Fatal("mismatched lengths accepted")
	}
	if _, err = SumProductCeil([]int64{-1}, []Micros{1}, 1); err != ErrNegative {
		t.Fatalf("negative quantity = %v", err)
	}
	if _, err = SumProductCeil([]int64{1}, []Micros{1}, 0); err == nil {
		t.Fatal("zero divisor accepted")
	}
	if _, err = SumProductCeil([]int64{math.MaxInt64, math.MaxInt64}, []Micros{math.MaxInt64, math.MaxInt64}, 1); err != ErrOverflow {
		t.Fatalf("overflow = %v", err)
	}
}

func TestFormat(t *testing.T) {
	if got := Format("CNY", 1_250_000); got != "CNY 1.250000" {
		t.Fatalf("format = %s", got)
	}
	if got := Format("USD", -1); got != "USD -0.000001" {
		t.Fatalf("negative format = %s", got)
	}
	if got := Format("USD", math.MinInt64); got != "USD -9223372036854.775808" {
		t.Fatalf("minimum format = %s", got)
	}
}
