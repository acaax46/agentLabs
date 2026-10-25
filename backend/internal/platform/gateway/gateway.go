// Package gateway is the trusted host's client for OpenAI-compatible chat models with tool
// calling. Credentials enter here and nowhere else in the execution path: the sandbox, the
// prompts and the run records never see them.
//
// The package classifies every failure by what is known about the request, because billing and
// retries depend on it: a request that never left the host can be retried, one the provider
// rejected produced nothing, and one that was written but not answered has an unknown outcome
// that must never be blindly resent.
package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptrace"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// Failure kinds, see the package comment.
const (
	NotSent  = "not_sent"
	Rejected = "rejected"
	Unknown  = "unknown"
	Invalid  = "invalid_response"
)

const (
	MaxRequestBytes  = 8 << 20
	MaxResponseBytes = 8 << 20
	MaxTools         = 16
	DefaultMaxTokens = 4096
	MaxMaxTokens     = 32768
)

// Error carries only values that are safe to show: the provider's free-text message can echo
// fragments of the API key, so it is never copied. Code is the provider's short error code.
type Error struct {
	Kind    string
	Status  int
	Code    string
	Message string
}

func (e *Error) Error() string {
	if e.Status != 0 {
		return fmt.Sprintf("model gateway %s (HTTP %d): %s", e.Kind, e.Status, e.Message)
	}
	return fmt.Sprintf("model gateway %s: %s", e.Kind, e.Message)
}

// ToolCall is a function call requested by the model.
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

type FunctionCall struct {
	Name string `json:"name"`
	// Arguments is a JSON document encoded as a string, exactly as models emit it.
	Arguments string `json:"arguments"`
}

// Message is one chat turn.
type Message struct {
	Role       string
	Content    string
	ToolCalls  []ToolCall
	ToolCallID string
	Name       string
}

// MarshalJSON writes "content": null for assistant turns that only call tools, which strict
// OpenAI-compatible servers require.
func (m Message) MarshalJSON() ([]byte, error) {
	wire := struct {
		Role       string     `json:"role"`
		Content    *string    `json:"content"`
		ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
		ToolCallID string     `json:"tool_call_id,omitempty"`
		Name       string     `json:"name,omitempty"`
	}{Role: m.Role, ToolCalls: m.ToolCalls, ToolCallID: m.ToolCallID, Name: m.Name}
	if m.Content != "" || len(m.ToolCalls) == 0 {
		content := m.Content
		wire.Content = &content
	}
	return json.Marshal(wire)
}

// UnmarshalJSON accepts what MarshalJSON writes so transcripts can be stored and restored.
func (m *Message) UnmarshalJSON(data []byte) error {
	var wire struct {
		Role       string          `json:"role"`
		Content    json.RawMessage `json:"content"`
		ToolCalls  []ToolCall      `json:"tool_calls"`
		ToolCallID string          `json:"tool_call_id"`
		Name       string          `json:"name"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	*m = Message{Role: wire.Role, Content: contentText(wire.Content), ToolCalls: wire.ToolCalls, ToolCallID: wire.ToolCallID, Name: wire.Name}
	return nil
}

// contentText reads a message body that is a string, null, or a list of typed text parts.
func contentText(raw json.RawMessage) string {
	text := bytes.TrimSpace(raw)
	if len(text) == 0 || string(text) == "null" {
		return ""
	}
	var plain string
	if json.Unmarshal(text, &plain) == nil {
		return plain
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(text, &parts) != nil {
		return ""
	}
	var builder strings.Builder
	for _, part := range parts {
		if part.Type == "text" || part.Type == "" {
			builder.WriteString(part.Text)
		}
	}
	return builder.String()
}

// Tool describes a function the model may call.
type Tool struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

type ToolFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

// Request is one completion. Conversations may receive text deltas; the completed result still
// carries provider-reported usage and failures after dispatch remain unknown.
type Request struct {
	// OnText receives only public assistant text, never reasoning tokens or tool arguments.
	OnText      func(string)
	Model       string
	Messages    []Message
	Tools       []Tool
	MaxTokens   int
	Temperature *float64
}

// Usage keeps unknown counts nil rather than zero.
type Usage struct {
	PromptTokens     *int64
	CompletionTokens *int64
	TotalTokens      *int64
	CachedTokens     *int64
}

// Complete reports whether the provider returned the counts needed to price a request.
func (u Usage) Complete() bool { return u.PromptTokens != nil && u.CompletionTokens != nil }

type Response struct {
	ID           string
	Model        string
	Message      Message
	FinishReason string
	Usage        Usage
	Latency      time.Duration
}

// Client talks to one connection. HTTP must already pin the approved origin and validate the
// resolved address on every dial (connection.Store.Client does).
type Client struct {
	BaseURL string
	APIKey  string
	HTTP    *http.Client
}

var codePattern = regexp.MustCompile(`[^A-Za-z0-9_.-]`)

func safeCode(value string) string {
	value = codePattern.ReplaceAllString(value, "")
	if len(value) > 64 {
		value = value[:64]
	}
	return value
}

// codeOf reads a provider error code that may be a string or a number.
func codeOf(value any) string {
	switch code := value.(type) {
	case string:
		return safeCode(code)
	case float64:
		return safeCode(strconv.FormatFloat(code, 'f', -1, 64))
	default:
		return ""
	}
}

type wireRequest struct {
	Model         string          `json:"model"`
	Messages      []Message       `json:"messages"`
	Tools         []Tool          `json:"tools,omitempty"`
	ToolChoice    string          `json:"tool_choice,omitempty"`
	MaxTokens     int             `json:"max_tokens"`
	Temperature   *float64        `json:"temperature,omitempty"`
	Stream        bool            `json:"stream"`
	StreamOptions map[string]bool `json:"stream_options,omitempty"`
}

type wireResponse struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		Message struct {
			Content   json.RawMessage `json:"content"`
			ToolCalls []ToolCall      `json:"tool_calls"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens        *int64 `json:"prompt_tokens"`
		CompletionTokens    *int64 `json:"completion_tokens"`
		TotalTokens         *int64 `json:"total_tokens"`
		PromptTokensDetails *struct {
			CachedTokens *int64 `json:"cached_tokens"`
		} `json:"prompt_tokens_details"`
	} `json:"usage"`
	Error *struct {
		Code any    `json:"code"`
		Type string `json:"type"`
	} `json:"error"`
}

func notSent(message string) *Error { return &Error{Kind: NotSent, Message: message} }

// Chat performs one completion and returns a *Error for every failure.
func (c *Client) Chat(ctx context.Context, request Request) (Response, error) {
	if c.HTTP == nil || strings.TrimSpace(c.BaseURL) == "" || strings.TrimSpace(c.APIKey) == "" {
		return Response{}, notSent("the model connection is not configured")
	}
	if strings.TrimSpace(request.Model) == "" || len(request.Messages) == 0 || len(request.Tools) > MaxTools {
		return Response{}, notSent("the request needs a model, messages and at most 16 tools")
	}
	if request.MaxTokens == 0 {
		request.MaxTokens = DefaultMaxTokens
	}
	if request.MaxTokens < 1 || request.MaxTokens > MaxMaxTokens {
		return Response{}, notSent("max tokens is out of range")
	}
	wire := wireRequest{Model: request.Model, Messages: request.Messages, Tools: request.Tools, MaxTokens: request.MaxTokens, Temperature: request.Temperature}
	if request.OnText != nil {
		wire.Stream = true
		wire.StreamOptions = map[string]bool{"include_usage": true}
	}
	if len(request.Tools) > 0 {
		wire.ToolChoice = "auto"
	}
	body, err := json.Marshal(wire)
	if err != nil {
		return Response{}, notSent("the request could not be encoded")
	}
	if len(body) > MaxRequestBytes {
		return Response{}, notSent("the request is larger than the model gateway accepts")
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.BaseURL, "/")+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return Response{}, notSent("the model endpoint address is invalid")
	}
	httpRequest.Header.Set("Authorization", "Bearer "+c.APIKey)
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "application/json")
	if request.OnText != nil {
		httpRequest.Header.Set("Accept", "text/event-stream")
	}
	httpRequest.Header.Set("User-Agent", "AgentLab-Gateway/1")
	// WroteRequest is the only reliable evidence that the provider may have seen the request.
	var written atomic.Bool
	trace := &httptrace.ClientTrace{WroteRequest: func(info httptrace.WroteRequestInfo) {
		if info.Err == nil {
			written.Store(true)
		}
	}}
	httpRequest = httpRequest.WithContext(httptrace.WithClientTrace(httpRequest.Context(), trace))
	started := time.Now()
	response, err := c.HTTP.Do(httpRequest)
	if err != nil {
		if !written.Load() {
			return Response{}, notSent("could not reach the model endpoint")
		}
		if errors.Is(err, context.Canceled) {
			return Response{}, &Error{Kind: Unknown, Message: "the request was cancelled after it was sent"}
		}
		return Response{}, &Error{Kind: Unknown, Message: "the model endpoint did not answer after the request was sent"}
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode >= 200 && response.StatusCode < 300 && strings.Contains(response.Header.Get("Content-Type"), "text/event-stream") {
		return readCompletionStream(response.Body, request.OnText, started)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, MaxResponseBytes+1))
	if err != nil || len(raw) > MaxResponseBytes {
		return Response{}, &Error{Kind: Unknown, Status: response.StatusCode, Message: "the model response could not be read completely"}
	}
	var decoded wireResponse
	decodeErr := json.Unmarshal(raw, &decoded)
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		failure := &Error{Kind: Rejected, Status: response.StatusCode, Message: "the model provider rejected the request"}
		if decodeErr == nil && decoded.Error != nil {
			failure.Code = codeOf(decoded.Error.Code)
			if failure.Code == "" {
				failure.Code = safeCode(decoded.Error.Type)
			}
		}
		return Response{}, failure
	}
	if decodeErr != nil || len(decoded.Choices) == 0 {
		return Response{}, &Error{Kind: Invalid, Status: response.StatusCode, Message: "the model response was not a usable chat completion"}
	}
	choice := decoded.Choices[0]
	message := Message{Role: "assistant", Content: contentText(choice.Message.Content), ToolCalls: choice.Message.ToolCalls}
	for i := range message.ToolCalls {
		call := &message.ToolCalls[i]
		if call.ID == "" {
			call.ID = fmt.Sprintf("call_%d", i+1)
		}
		call.Type = "function"
		if strings.TrimSpace(call.Function.Name) == "" {
			return Response{}, &Error{Kind: Invalid, Status: response.StatusCode, Message: "the model requested a tool call without a name"}
		}
	}
	result := Response{ID: decoded.ID, Model: decoded.Model, Message: message, FinishReason: choice.FinishReason, Latency: time.Since(started)}
	if decoded.Usage != nil {
		usage := Usage{PromptTokens: decoded.Usage.PromptTokens, CompletionTokens: decoded.Usage.CompletionTokens, TotalTokens: decoded.Usage.TotalTokens}
		if decoded.Usage.PromptTokensDetails != nil {
			usage.CachedTokens = decoded.Usage.PromptTokensDetails.CachedTokens
		}
		if !usage.valid() {
			return Response{}, &Error{Kind: Invalid, Status: response.StatusCode, Message: "the model reported impossible token counts"}
		}
		result.Usage = usage
	}
	return result, nil
}

func (u Usage) valid() bool {
	for _, count := range []*int64{u.PromptTokens, u.CompletionTokens, u.TotalTokens, u.CachedTokens} {
		if count != nil && *count < 0 {
			return false
		}
	}
	return u.CachedTokens == nil || u.PromptTokens == nil || *u.CachedTokens <= *u.PromptTokens
}
