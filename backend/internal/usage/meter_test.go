package usage

import (
	"strings"
	"testing"
)

func TestMeterResponsesFrames(t *testing.T) {
	meter := NewMeter("responses")
	if meter.Protocol != "responses" || meter.Usage.Source != "unknown" {
		t.Fatalf("meter = %+v", meter)
	}
	frame := `{"type":"response.completed","response":{"id":"response-1","model":"model-1","status":"completed","usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15,"input_tokens_details":{"cached_tokens":4},"output_tokens_details":{"reasoning_tokens":2}},"output":[{"type":"message"},{"type":"reasoning"},{"type":"function_call"}]}}`
	if err := meter.Observe([]byte(frame)); err != nil {
		t.Fatal(err)
	}
	if !meter.Complete || meter.ResponseID != "response-1" || meter.Model != "model-1" || *meter.Usage.InputTokens != 10 || *meter.Usage.OutputTokens != 5 || *meter.Usage.CachedInputTokens != 4 || *meter.Usage.ReasoningTokens != 2 || meter.Usage.UnpricedDimensions {
		t.Fatalf("usage = %+v", meter)
	}
	unknown := NewMeter("responses")
	if err := unknown.Observe([]byte(`{"type":"response.completed","response":{"id":"r","model":"m","usage":{"input_tokens":1,"output_tokens":1},"output":[{"type":"computer_call"}]}}`)); err != nil {
		t.Fatal(err)
	}
	if !unknown.Usage.UnpricedDimensions || !unknown.Complete {
		t.Fatalf("unknown dimensions = %+v", unknown)
	}
	topLevel := NewMeter("responses")
	if err := topLevel.Observe([]byte(`{"type":"response.completed","id":"r","model":"m","usage":{"input_tokens":1,"output_tokens":1}}`)); err != nil {
		t.Fatal(err)
	}
	if topLevel.ResponseID != "r" || !topLevel.Complete {
		t.Fatalf("top level = %+v", topLevel)
	}
}

func TestMeterAnthropicFrames(t *testing.T) {
	meter := NewMeter("anthropic-messages")
	frame := `{"type":"message","message":{"id":"message-1","model":"claude","usage":{"input_tokens":10,"output_tokens":5,"cache_read_input_tokens":4,"cache_creation_input_tokens":3,"cache_creation":{"ephemeral_1h_input_tokens":100},"server_tool_use":{"web_search_requests":1}}}}`
	if err := meter.Observe([]byte(frame)); err != nil {
		t.Fatal(err)
	}
	if !meter.Complete || meter.ResponseID != "message-1" || meter.Model != "claude" || *meter.Usage.InputTokens != 17 || *meter.Usage.CachedInputTokens != 4 || *meter.Usage.CacheWriteTokens != 3 || *meter.Usage.OutputTokens != 5 || *meter.Usage.TotalTokens != 22 || !meter.Usage.UnpricedDimensions {
		t.Fatalf("usage = %+v", meter)
	}
	plain := NewMeter("anthropic-messages")
	if err := plain.Observe([]byte(`{"type":"message_stop","message":{"id":"m","model":"c","usage":{"input_tokens":1,"output_tokens":2}}}`)); err != nil {
		t.Fatal(err)
	}
	if !plain.Complete || *plain.Usage.TotalTokens != 3 {
		t.Fatalf("plain = %+v", plain)
	}
}

func TestMeterRejectsInvalidFrames(t *testing.T) {
	cases := []struct {
		name     string
		protocol string
		frame    string
	}{
		{"unsupported", "unknown", `{}`},
		{"invalid json", "responses", `{`},
		{"too large", "responses", strings.Repeat("x", 4<<20+1)},
		{"negative", "responses", `{"type":"response.completed","response":{"id":"r","usage":{"input_tokens":-1,"output_tokens":1}}}`},
		{"cached exceeds", "responses", `{"type":"response.completed","response":{"id":"r","usage":{"input_tokens":1,"output_tokens":1,"input_tokens_details":{"cached_tokens":2}}}}`},
		{"anthropic negative", "anthropic-messages", `{"type":"message","message":{"id":"r","usage":{"input_tokens":-1,"output_tokens":1}}}`},
		{"anthropic overflow", "anthropic-messages", `{"type":"message","message":{"id":"r","usage":{"input_tokens":9223372036854775807,"cache_read_input_tokens":1,"output_tokens":1}}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := NewMeter(tc.protocol).Observe([]byte(tc.frame)); err == nil {
				t.Fatal("invalid frame accepted")
			}
		})
	}
	meter := NewMeter("responses")
	if err := meter.Observe([]byte(`{"type":"response.completed","response":{"id":"one","usage":{"input_tokens":1,"output_tokens":1}}}`)); err != nil {
		t.Fatal(err)
	}
	if err := meter.Observe([]byte(`{"type":"response.completed","response":{"id":"two","usage":{"input_tokens":1,"output_tokens":1}}}`)); err == nil {
		t.Fatal("response identity changed")
	}
	price := Price{Version: "v1", Currency: "USD", Input: 1, Output: 1}
	if _, err := price.Cost(Usage{Source: "provider-reported", InputTokens: ptr(10), OutputTokens: ptr(5), CachedInputTokens: ptr(11)}); err == nil {
		t.Fatal("cache subset overflow accepted")
	}
}

func TestMeterNestedInvalidFrames(t *testing.T) {
	if err := NewMeter("responses").Observe([]byte(`{"response":{"usage":}}`)); err == nil {
		t.Fatal("invalid response frame accepted")
	}
	if err := NewMeter("anthropic-messages").Observe([]byte(`{"message":{"usage":}}`)); err == nil {
		t.Fatal("invalid message frame accepted")
	}
	if err := NewMeter("anthropic-messages").Observe([]byte(`{"type":"message","message":{"id":"r","usage":{"input_tokens":9223372036854775807,"output_tokens":1}}}`)); err == nil {
		t.Fatal("overflow frame accepted")
	}
}

func TestMeterRawArrayFrames(t *testing.T) {
	if err := NewMeter("responses").Observe([]byte(`{"response":[]}`)); err == nil {
		t.Fatal("array response accepted")
	}
	if err := NewMeter("anthropic-messages").Observe([]byte(`{"message":[]}`)); err == nil {
		t.Fatal("array message accepted")
	}
}
