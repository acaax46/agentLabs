package usage

import (
	"encoding/json"
	"fmt"
	"math/big"
)

// Meter consumes provider JSON frames after transport decoding. It never receives credentials.
// Replayed cumulative frames replace counters; they are not added to existing totals.
type Meter struct {
	Protocol   string
	Model      string
	ResponseID string
	Usage      Usage
	Complete   bool
}

func NewMeter(protocol string) *Meter {
	return &Meter{Protocol: protocol, Usage: Usage{Source: "unknown"}}
}

func (m *Meter) Observe(data []byte) error {
	if len(data) > 4<<20 {
		return fmt.Errorf("usage frame too large")
	}
	var frame struct {
		Type     string          `json:"type"`
		ID       string          `json:"id"`
		Model    string          `json:"model"`
		Usage    json.RawMessage `json:"usage"`
		Response json.RawMessage `json:"response"`
		Message  json.RawMessage `json:"message"`
	}
	if err := json.Unmarshal(data, &frame); err != nil {
		return err
	}
	next := *m
	next.Usage = cloneUsage(m.Usage)
	switch m.Protocol {
	case "responses":
		raw := data
		if len(frame.Response) > 0 && string(frame.Response) != "null" {
			raw = frame.Response
		}
		var response struct {
			OutputItems []struct {
				Type string `json:"type"`
			} `json:"output"`
			ID     string `json:"id"`
			Model  string `json:"model"`
			Status string `json:"status"`
			Usage  *struct {
				Input        *int64 `json:"input_tokens"`
				Output       *int64 `json:"output_tokens"`
				Total        *int64 `json:"total_tokens"`
				InputDetails struct {
					Cached *int64 `json:"cached_tokens"`
				} `json:"input_tokens_details"`
				OutputDetails struct {
					Reasoning *int64 `json:"reasoning_tokens"`
				} `json:"output_tokens_details"`
			} `json:"usage"`
		}
		if err := json.Unmarshal(raw, &response); err != nil {
			return err
		}
		if response.Model != "" {
			next.Model = response.Model
		}
		if response.ID != "" {
			next.ResponseID = response.ID
		}
		if response.Usage != nil {
			u := response.Usage
			next.Usage = Usage{InputTokens: u.Input, OutputTokens: u.Output, TotalTokens: u.Total, CachedInputTokens: u.InputDetails.Cached, ReasoningTokens: u.OutputDetails.Reasoning, Source: "provider-reported"}
		}
		for _, item := range response.OutputItems {
			if item.Type != "message" && item.Type != "reasoning" && item.Type != "function_call" {
				next.Usage.UnpricedDimensions = true
			}
		}
		next.Complete = next.Complete || frame.Type == "response.completed" || response.Status == "completed"
	case "anthropic-messages":
		raw := data
		if len(frame.Message) > 0 && string(frame.Message) != "null" {
			raw = frame.Message
		}
		var message struct {
			ID    string `json:"id"`
			Model string `json:"model"`
			Usage *struct {
				Input         *int64 `json:"input_tokens"`
				Output        *int64 `json:"output_tokens"`
				Read          *int64 `json:"cache_read_input_tokens"`
				Write         *int64 `json:"cache_creation_input_tokens"`
				CacheCreation struct {
					Long int64 `json:"ephemeral_1h_input_tokens"`
				} `json:"cache_creation"`
				ServerToolUse json.RawMessage `json:"server_tool_use"`
			} `json:"usage"`
		}
		if err := json.Unmarshal(raw, &message); err != nil {
			return err
		}
		if message.Model != "" {
			next.Model = message.Model
		}
		if message.ID != "" {
			next.ResponseID = message.ID
		}
		if message.Usage != nil {
			u := message.Usage
			for _, count := range []*int64{u.Input, u.Output, u.Read, u.Write} {
				if count != nil && *count < 0 {
					return fmt.Errorf("negative provider count")
				}
			}
			if u.CacheCreation.Long != 0 || len(u.ServerToolUse) > 0 {
				next.Usage.UnpricedDimensions = true
			}
			if u.Input != nil {
				sum := big.NewInt(*u.Input)
				if u.Read != nil {
					sum.Add(sum, big.NewInt(*u.Read))
				}
				if u.Write != nil {
					sum.Add(sum, big.NewInt(*u.Write))
				}
				if !sum.IsInt64() {
					return fmt.Errorf("usage overflow")
				}
				n := sum.Int64()
				next.Usage.InputTokens = &n
				next.Usage.CachedInputTokens = u.Read
				next.Usage.CacheWriteTokens = u.Write
			}
			if u.Output != nil {
				next.Usage.OutputTokens = u.Output
			}
			next.Usage.Source = "provider-reported"
			if next.Usage.InputTokens != nil && next.Usage.OutputTokens != nil {
				total := new(big.Int).Add(big.NewInt(*next.Usage.InputTokens), big.NewInt(*next.Usage.OutputTokens))
				if !total.IsInt64() {
					return fmt.Errorf("usage overflow")
				}
				v := total.Int64()
				next.Usage.TotalTokens = &v
			}
		}
		next.Complete = next.Complete || frame.Type == "message_stop" || frame.Type == "message"
	default:
		return fmt.Errorf("unsupported usage protocol")
	}
	if err := next.Usage.validate(); err != nil {
		return err
	}
	if m.ResponseID != "" && next.ResponseID != m.ResponseID {
		return fmt.Errorf("response identity changed within attempt")
	}
	*m = next
	return nil
}

// Price uses micro-currency units per million tokens. Cached/reasoning tokens are subsets.
type Price struct {
	Version, Currency                     string
	Input, CachedRead, CacheWrite, Output int64
}

func (p Price) Cost(u Usage) (int64, error) {
	if err := u.validate(); err != nil {
		return 0, err
	}
	if p.Version == "" || p.Currency == "" || u.Source != "provider-reported" || u.InputTokens == nil || u.OutputTokens == nil || u.UnpricedDimensions {
		return 0, fmt.Errorf("final provider usage and price version required")
	}
	for _, rate := range []int64{p.Input, p.CachedRead, p.CacheWrite, p.Output} {
		if rate < 0 {
			return 0, fmt.Errorf("negative price")
		}
	}
	read, write := int64(0), int64(0)
	if u.CachedInputTokens != nil {
		read = *u.CachedInputTokens
	}
	if u.CacheWriteTokens != nil {
		write = *u.CacheWriteTokens
	}
	if read > *u.InputTokens || write > *u.InputTokens-read {
		return 0, fmt.Errorf("cache subsets exceed input")
	}
	amount := new(big.Int)
	for _, item := range [][2]int64{{*u.InputTokens - read - write, p.Input}, {read, p.CachedRead}, {write, p.CacheWrite}, {*u.OutputTokens, p.Output}} {
		amount.Add(amount, new(big.Int).Mul(big.NewInt(item[0]), big.NewInt(item[1])))
	}
	amount.Add(amount, big.NewInt(999999))
	amount.Quo(amount, big.NewInt(1000000))
	if !amount.IsInt64() {
		return 0, fmt.Errorf("cost overflow")
	}
	return amount.Int64(), nil
}
