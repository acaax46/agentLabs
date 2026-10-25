// Package money holds integer micro-unit arithmetic. Amounts are never floating point.
package money

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"strconv"
)

// Micros is an amount in millionths of a currency unit. JSON carries it as a decimal string
// so JavaScript clients never round it through a double.
type Micros int64

var (
	ErrOverflow = errors.New("money amount overflow")
	ErrNegative = errors.New("money amount must not be negative")
)

func (m Micros) MarshalJSON() ([]byte, error) {
	return []byte(`"` + strconv.FormatInt(int64(m), 10) + `"`), nil
}

func (m *Micros) UnmarshalJSON(data []byte) error {
	// By encoding/json convention a JSON null is a no-op for non-pointer values.
	if string(data) == "null" {
		return nil
	}
	var text string
	if err := json.Unmarshal(data, &text); err != nil {
		return fmt.Errorf("money amount must be a decimal string")
	}
	value, err := strconv.ParseInt(text, 10, 64)
	if err != nil {
		return fmt.Errorf("money amount must be a decimal string")
	}
	*m = Micros(value)
	return nil
}

func (m Micros) String() string { return strconv.FormatInt(int64(m), 10) }

// Add returns a+b and fails instead of wrapping.
func Add(a, b Micros) (Micros, error) {
	if (b > 0 && a > math.MaxInt64-b) || (b < 0 && a < math.MinInt64-b) {
		return 0, ErrOverflow
	}
	return a + b, nil
}

// Sum adds every amount in order.
func Sum(values ...Micros) (Micros, error) {
	var total Micros
	for _, value := range values {
		next, err := Add(total, value)
		if err != nil {
			return 0, err
		}
		total = next
	}
	return total, nil
}

// MulDivCeil returns ceil(quantity*rate/divisor) without intermediate overflow.
// It is the one rounding rule for every meter: partial units always round up.
func MulDivCeil(quantity int64, rate Micros, divisor int64) (Micros, error) {
	if quantity < 0 || rate < 0 {
		return 0, ErrNegative
	}
	if divisor < 1 {
		return 0, fmt.Errorf("divisor must be positive")
	}
	product := new(big.Int).Mul(big.NewInt(quantity), big.NewInt(int64(rate)))
	product.Add(product, big.NewInt(divisor-1))
	product.Quo(product, big.NewInt(divisor))
	if !product.IsInt64() {
		return 0, ErrOverflow
	}
	return Micros(product.Int64()), nil
}

// SumProductCeil returns ceil(sum(quantities[i]*rates[i]) / divisor) in one rounding step, so
// pricing several dimensions never rounds each part up separately.
func SumProductCeil(quantities []int64, rates []Micros, divisor int64) (Micros, error) {
	if len(quantities) != len(rates) {
		return 0, fmt.Errorf("quantities and rates must pair up")
	}
	if divisor < 1 {
		return 0, fmt.Errorf("divisor must be positive")
	}
	total := new(big.Int)
	for i, quantity := range quantities {
		if quantity < 0 || rates[i] < 0 {
			return 0, ErrNegative
		}
		total.Add(total, new(big.Int).Mul(big.NewInt(quantity), big.NewInt(int64(rates[i]))))
	}
	total.Add(total, big.NewInt(divisor-1))
	total.Quo(total, big.NewInt(divisor))
	if !total.IsInt64() {
		return 0, ErrOverflow
	}
	return Micros(total.Int64()), nil
}

// Format renders an amount for diagnostics, for example "CNY 1.250000".
func Format(currency string, value Micros) string {
	sign := ""
	units := int64(value)
	if units < 0 {
		sign = "-"
		// Negating math.MinInt64 overflows, so work on the unsigned magnitude.
		magnitude := uint64(-(units + 1)) + 1
		return fmt.Sprintf("%s %s%d.%06d", currency, sign, magnitude/1_000_000, magnitude%1_000_000)
	}
	return fmt.Sprintf("%s %d.%06d", currency, units/1_000_000, units%1_000_000)
}
