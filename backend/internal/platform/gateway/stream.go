package gateway

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
)

// A broken stream after dispatch is an unknown outcome; the caller must retain its reservation.
// Usage comes from the provider's final chunk and is never estimated from text deltas.
func readCompletionStream(reader io.Reader, onText func(string), started time.Time) (Response, error) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), MaxResponseBytes)
	result := Response{Message: Message{Role: "assistant"}}
	calls := map[int]*ToolCall{}
	var content strings.Builder
	var data []string
	total, done, finished := 0, false, false
	invalid := func() (Response, error) {
		return Response{}, &Error{Kind: Unknown, Message: "the model stream ended before a complete response was received"}
	}
	consume := func(payload string) bool {
		if payload == "[DONE]" {
			done = true
			return true
		}
		var chunk struct {
			Error   json.RawMessage `json:"error"`
			ID      string          `json:"id"`
			Model   string          `json:"model"`
			Usage   json.RawMessage `json:"usage"`
			Choices []struct {
				Index int `json:"index"`
				Delta struct {
					Content   string `json:"content"`
					ToolCalls []struct {
						Index    int          `json:"index"`
						ID       string       `json:"id"`
						Function FunctionCall `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
				FinishReason *string `json:"finish_reason"`
			} `json:"choices"`
		}
		if json.Unmarshal([]byte(payload), &chunk) != nil {
			return false
		}
		if len(chunk.Error) > 0 && string(chunk.Error) != "null" {
			return false
		}
		if chunk.ID != "" {
			result.ID = chunk.ID
		}
		if chunk.Model != "" {
			result.Model = chunk.Model
		}
		if len(chunk.Usage) > 0 && string(chunk.Usage) != "null" {
			var wire wireResponse
			if json.Unmarshal([]byte(`{"usage":`+string(chunk.Usage)+`}`), &wire) != nil || wire.Usage == nil {
				return false
			}
			result.Usage = Usage{PromptTokens: wire.Usage.PromptTokens, CompletionTokens: wire.Usage.CompletionTokens, TotalTokens: wire.Usage.TotalTokens}
			if wire.Usage.PromptTokensDetails != nil {
				result.Usage.CachedTokens = wire.Usage.PromptTokensDetails.CachedTokens
			}
			if !result.Usage.valid() {
				return false
			}
		}
		for _, choice := range chunk.Choices {
			if choice.Index != 0 {
				continue
			}
			if choice.Delta.Content != "" {
				content.WriteString(choice.Delta.Content)
				if onText != nil {
					onText(choice.Delta.Content)
				}
			}
			for _, delta := range choice.Delta.ToolCalls {
				if delta.Index < 0 || delta.Index >= MaxTools {
					return false
				}
				call := calls[delta.Index]
				if call == nil {
					call = &ToolCall{Type: "function"}
					calls[delta.Index] = call
				}
				call.ID += delta.ID
				call.Function.Name += delta.Function.Name
				call.Function.Arguments += delta.Function.Arguments
			}
			if choice.FinishReason != nil {
				finished = true
				result.FinishReason = *choice.FinishReason
			}
		}
		return true
	}
	for scanner.Scan() {
		line := scanner.Text()
		total += len(line)
		if total > MaxResponseBytes {
			return invalid()
		}
		if strings.HasPrefix(line, "data:") {
			data = append(data, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
		if line == "" && len(data) > 0 {
			if !consume(strings.Join(data, "\n")) {
				return invalid()
			}
			data = nil
			if done {
				break
			}
		}
	}
	if len(data) > 0 && !consume(strings.Join(data, "\n")) {
		return invalid()
	}
	if scanner.Err() != nil || !done || !finished {
		return invalid()
	}
	result.Message.Content = content.String()
	indexes := make([]int, 0, len(calls))
	for index := range calls {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	for _, index := range indexes {
		call := calls[index]
		if strings.TrimSpace(call.Function.Name) == "" {
			return invalid()
		}
		if call.ID == "" {
			call.ID = fmt.Sprintf("call_%d", index+1)
		}
		result.Message.ToolCalls = append(result.Message.ToolCalls, *call)
	}
	result.Latency = time.Since(started)
	return result, nil
}
