package usage

import (
	"math"
	"testing"
)

func TestPriceCostRejectsInvalidEvidence(t *testing.T) {
	valid := Usage{Source: "provider-reported", InputTokens: ptr(10), OutputTokens: ptr(5)}
	price := Price{Version: "v1", Currency: "USD", Input: 1, CachedRead: 1, CacheWrite: 1, Output: 1}
	if amount, err := price.Cost(valid); err != nil || amount != 1 {
		t.Fatalf("cost = %d %v", amount, err)
	}
	cases := []struct {
		name  string
		price Price
		usage Usage
	}{
		{"version", Price{Currency: "USD"}, valid},
		{"currency", Price{Version: "v1"}, valid},
		{"source", price, Usage{Source: "estimated", InputTokens: ptr(1), OutputTokens: ptr(1)}},
		{"missing input", price, Usage{Source: "provider-reported", OutputTokens: ptr(1)}},
		{"missing output", price, Usage{Source: "provider-reported", InputTokens: ptr(1)}},
		{"unpriced", price, Usage{Source: "provider-reported", InputTokens: ptr(1), OutputTokens: ptr(1), UnpricedDimensions: true}},
		{"negative rate", Price{Version: "v1", Currency: "USD", Input: -1}, valid},
		{"negative usage", price, Usage{Source: "provider-reported", InputTokens: ptr(-1), OutputTokens: ptr(1)}},
		{"cached exceeds", price, Usage{Source: "provider-reported", InputTokens: ptr(10), OutputTokens: ptr(1), CachedInputTokens: ptr(11)}},
		{"write exceeds", price, Usage{Source: "provider-reported", InputTokens: ptr(10), OutputTokens: ptr(1), CachedInputTokens: ptr(5), CacheWriteTokens: ptr(6)}},
		{"overflow", Price{Version: "v1", Currency: "USD", Input: math.MaxInt64, Output: 1}, Usage{Source: "provider-reported", InputTokens: ptr(math.MaxInt64), OutputTokens: ptr(1)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := tc.price.Cost(tc.usage); err == nil {
				t.Fatal("invalid cost accepted")
			}
		})
	}
}

func TestUsageValidateRejectsInvalidSubsets(t *testing.T) {
	cases := []Usage{
		{Source: "invalid"},
		{Source: "unknown", InputTokens: ptr(-1)},
		{Source: "unknown", InputTokens: ptr(1), CachedInputTokens: ptr(2)},
		{Source: "unknown", OutputTokens: ptr(1), ReasoningTokens: ptr(2)},
	}
	for _, usage := range cases {
		if err := usage.validate(); err == nil {
			t.Fatalf("invalid usage accepted: %+v", usage)
		}
	}
	if err := (Usage{Source: "provider-reported", InputTokens: ptr(10), CachedInputTokens: ptr(4), OutputTokens: ptr(5), ReasoningTokens: ptr(2)}).validate(); err != nil {
		t.Fatal(err)
	}
}
