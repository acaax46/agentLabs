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
	"strings"
	"sync/atomic"
	"time"
)

// Protocols a code-agent harness may speak through the model channel. The trusted host forwards a
// request to the connection's own origin with the real credential; the sandboxed CLI that wrote
// the request never learns either, and it cannot widen what the request may cost.
const (
	ProtocolChat      = "chat"      // OpenAI Chat Completions: pi, OpenCode
	ProtocolResponses = "responses" // OpenAI Responses: Codex CLI
	ProtocolMessages  = "messages"  // Anthropic Messages: Claude Code
)

var protocolPaths = map[string]string{ProtocolChat: "/chat/completions", ProtocolResponses: "/responses", ProtocolMessages: "/messages"}

// Fields a guest may set, per protocol. Everything else is dropped: provider-side state, routing to
// other models or providers, plugins, user identifiers and anything that could change the bill.
var allowedFields = map[string][]string{
	ProtocolChat:      {"messages", "tools", "tool_choice", "parallel_tool_calls", "temperature", "top_p", "stop", "response_format", "seed", "frequency_penalty", "presence_penalty"},
	ProtocolResponses: {"input", "instructions", "tools", "tool_choice", "parallel_tool_calls", "temperature", "top_p", "reasoning", "text", "truncation", "include"},
	ProtocolMessages:  {"messages", "system", "tools", "tool_choice", "temperature", "top_p", "top_k", "stop_sequences"},
}

// maxTokenFields is where each protocol reads the requested output cap.
var maxTokenFields = map[string][]string{
	ProtocolChat:      {"max_tokens", "max_completion_tokens"},
	ProtocolResponses: {"max_output_tokens"},
	ProtocolMessages:  {"max_tokens"},
}

// Content kinds the channel refuses: the platform prices text tokens only.
var mediaTypes = map[string]bool{"image": true, "image_url": true, "input_image": true, "input_file": true, "input_audio": true, "file": true, "document": true, "audio": true}

// Tool types that run on the provider side (search, code interpreters, MCP connectors). They carry
// their own fees or reach the network, so they are removed; the guest's own tools remain.
func clientTool(protocol string, tool map[string]any) bool {
	kind, _ := tool["type"].(string)
	switch protocol {
	case ProtocolChat:
		return kind == "function"
	case ProtocolResponses:
		return kind == "function" || kind == "custom" || kind == "local_shell"
	case ProtocolMessages:
		return kind == "" || kind == "custom"
	}
	return false
}

// MaxForwardTools bounds the tool list of one request.
const MaxForwardTools = 128

// Prepared is a guest request rewritten for the provider.
type Prepared struct {
	Body      []byte
	MaxTokens int
	// InputBytes approximates the prompt for a worst-case cost estimate before sending.
	InputBytes int
}

// Refusal is a guest request the channel will not forward. Message is safe to show the guest.
type Refusal struct{ Message string }

func (r *Refusal) Error() string { return "model channel refused the request: " + r.Message }

func refuse(format string, args ...any) error { return &Refusal{Message: fmt.Sprintf(format, args...)} }

// Prepare validates an untrusted harness request and rewrites it so it can use only the run's model,
// at most maxTokens output tokens, no streaming and no provider-side state or paid server tools.
func Prepare(protocol string, raw []byte, model string, maxTokens int) (Prepared, error) {
	allowed, known := allowedFields[protocol]
	if !known {
		return Prepared{}, refuse("unsupported protocol %q", protocol)
	}
	if len(raw) > MaxRequestBytes {
		return Prepared{}, refuse("the request is larger than %d bytes", MaxRequestBytes)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return Prepared{}, refuse("the request body must be a JSON object")
	}
	out := map[string]any{}
	for _, name := range allowed {
		if value, ok := fields[name]; ok && string(value) != "null" {
			out[name] = json.RawMessage(value)
		}
	}
	conversation := "messages"
	if protocol == ProtocolResponses {
		conversation = "input"
	}
	if len(fields[conversation]) == 0 {
		return Prepared{}, refuse("the request has no %s", conversation)
	}
	var walk any
	if err := json.Unmarshal(fields[conversation], &walk); err != nil {
		return Prepared{}, refuse("%s is not valid JSON", conversation)
	}
	if media := findMedia(walk); media != "" {
		return Prepared{}, refuse("only text content is accepted, found %q", media)
	}
	if raw, ok := fields["system"]; ok && protocol == ProtocolMessages {
		var system any
		if json.Unmarshal(raw, &system) == nil && findMedia(system) != "" {
			return Prepared{}, refuse("only text content is accepted in system")
		}
	}
	if rawTools, ok := fields["tools"]; ok && string(rawTools) != "null" {
		var tools []map[string]any
		if err := json.Unmarshal(rawTools, &tools); err != nil {
			return Prepared{}, refuse("tools must be a list of objects")
		}
		kept := make([]map[string]any, 0, len(tools))
		for _, tool := range tools {
			if clientTool(protocol, tool) {
				kept = append(kept, tool)
			}
		}
		if len(kept) > MaxForwardTools {
			return Prepared{}, refuse("at most %d tools are accepted", MaxForwardTools)
		}
		if len(kept) == 0 {
			delete(out, "tools")
			delete(out, "tool_choice")
			delete(out, "parallel_tool_calls")
		} else {
			out["tools"] = kept
		}
	}
	if protocol == ProtocolResponses {
		// Encrypted reasoning is the only extra a stateless conversation needs; nothing is stored.
		out["store"] = false
		if _, ok := out["include"]; ok {
			out["include"] = []string{"reasoning.encrypted_content"}
		}
	}
	effective := maxTokens
	for _, name := range maxTokenFields[protocol] {
		var requested int
		if value, ok := fields[name]; ok && json.Unmarshal(value, &requested) == nil && requested > 0 && requested < effective {
			effective = requested
		}
	}
	if effective < 1 || effective > MaxMaxTokens {
		return Prepared{}, refuse("max tokens is out of range")
	}
	out[maxTokenFields[protocol][0]] = effective
	out["model"] = model
	out["stream"] = false
	body, err := json.Marshal(out)
	if err != nil || len(body) > MaxRequestBytes {
		return Prepared{}, refuse("the request could not be encoded within %d bytes", MaxRequestBytes)
	}
	return Prepared{Body: body, MaxTokens: effective, InputBytes: len(body)}, nil
}

// findMedia returns the first non-text content type anywhere in a conversation.
func findMedia(value any) string {
	switch v := value.(type) {
	case map[string]any:
		if kind, ok := v["type"].(string); ok && mediaTypes[kind] {
			return kind
		}
		if _, ok := v["image_url"]; ok {
			return "image_url"
		}
		for _, child := range v {
			if found := findMedia(child); found != "" {
				return found
			}
		}
	case []any:
		for _, child := range v {
			if found := findMedia(child); found != "" {
				return found
			}
		}
	}
	return ""
}

// Forwarded is one provider answer to a prepared request.
type Forwarded struct {
	Body         []byte
	ID           string
	Model        string
	FinishReason string
	Usage        Usage
	Latency      time.Duration
}

// Forward sends a prepared request and classifies every failure exactly as Chat does: not sent,
// rejected (nothing to pay), or unknown (sent, outcome unknowable; never resend).
func (c *Client) Forward(ctx context.Context, protocol string, prepared Prepared) (Forwarded, error) {
	path, known := protocolPaths[protocol]
	if !known {
		return Forwarded{}, notSent("unsupported protocol")
	}
	if c.HTTP == nil || strings.TrimSpace(c.BaseURL) == "" || strings.TrimSpace(c.APIKey) == "" {
		return Forwarded{}, notSent("the model connection is not configured")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.BaseURL, "/")+path, bytes.NewReader(prepared.Body))
	if err != nil {
		return Forwarded{}, notSent("the model endpoint address is invalid")
	}
	request.Header.Set("Authorization", "Bearer "+c.APIKey)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "AgentLab-Gateway/1")
	if protocol == ProtocolMessages {
		request.Header.Set("x-api-key", c.APIKey)
		request.Header.Set("anthropic-version", "2023-06-01")
	}
	var written atomic.Bool
	trace := &httptrace.ClientTrace{WroteRequest: func(info httptrace.WroteRequestInfo) {
		if info.Err == nil {
			written.Store(true)
		}
	}}
	request = request.WithContext(httptrace.WithClientTrace(request.Context(), trace))
	started := time.Now()
	response, err := c.HTTP.Do(request)
	if err != nil {
		if !written.Load() {
			return Forwarded{}, notSent("could not reach the model endpoint")
		}
		if errors.Is(err, context.Canceled) {
			return Forwarded{}, &Error{Kind: Unknown, Message: "the request was cancelled after it was sent"}
		}
		return Forwarded{}, &Error{Kind: Unknown, Message: "the model endpoint did not answer after the request was sent"}
	}
	defer func() { _ = response.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(response.Body, MaxResponseBytes+1))
	if err != nil || len(raw) > MaxResponseBytes {
		return Forwarded{}, &Error{Kind: Unknown, Status: response.StatusCode, Message: "the model response could not be read completely"}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		failure := &Error{Kind: Rejected, Status: response.StatusCode, Message: "the model provider rejected the request"}
		var decoded struct {
			Error *struct {
				Code any    `json:"code"`
				Type string `json:"type"`
			} `json:"error"`
		}
		if json.Unmarshal(raw, &decoded) == nil && decoded.Error != nil {
			if failure.Code = codeOf(decoded.Error.Code); failure.Code == "" {
				failure.Code = safeCode(decoded.Error.Type)
			}
		}
		return Forwarded{}, failure
	}
	result, err := meter(protocol, raw)
	if err != nil {
		return Forwarded{}, &Error{Kind: Invalid, Status: response.StatusCode, Message: err.Error()}
	}
	result.Body, result.Latency = raw, time.Since(started)
	return result, nil
}

// meter reads identity, finish reason and token usage from a provider answer. Unknown counts stay nil.
func meter(protocol string, raw []byte) (Forwarded, error) {
	var result Forwarded
	switch protocol {
	case ProtocolChat:
		var body struct {
			ID      string `json:"id"`
			Model   string `json:"model"`
			Choices []struct {
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
		}
		if json.Unmarshal(raw, &body) != nil || len(body.Choices) == 0 {
			return result, errors.New("the model response was not a usable chat completion")
		}
		result.ID, result.Model, result.FinishReason = body.ID, body.Model, body.Choices[0].FinishReason
		if body.Usage != nil {
			result.Usage = Usage{PromptTokens: body.Usage.PromptTokens, CompletionTokens: body.Usage.CompletionTokens, TotalTokens: body.Usage.TotalTokens}
			if body.Usage.PromptTokensDetails != nil {
				result.Usage.CachedTokens = body.Usage.PromptTokensDetails.CachedTokens
			}
		}
	case ProtocolResponses:
		var body struct {
			ID     string          `json:"id"`
			Model  string          `json:"model"`
			Status string          `json:"status"`
			Output json.RawMessage `json:"output"`
			Usage  *struct {
				InputTokens        *int64 `json:"input_tokens"`
				OutputTokens       *int64 `json:"output_tokens"`
				TotalTokens        *int64 `json:"total_tokens"`
				InputTokensDetails *struct {
					CachedTokens *int64 `json:"cached_tokens"`
				} `json:"input_tokens_details"`
			} `json:"usage"`
		}
		if json.Unmarshal(raw, &body) != nil || len(body.Output) == 0 || body.Output[0] != '[' {
			return result, errors.New("the model response was not a usable response object")
		}
		result.ID, result.Model, result.FinishReason = body.ID, body.Model, body.Status
		if body.Usage != nil {
			result.Usage = Usage{PromptTokens: body.Usage.InputTokens, CompletionTokens: body.Usage.OutputTokens, TotalTokens: body.Usage.TotalTokens}
			if body.Usage.InputTokensDetails != nil {
				result.Usage.CachedTokens = body.Usage.InputTokensDetails.CachedTokens
			}
		}
	case ProtocolMessages:
		var body struct {
			ID         string          `json:"id"`
			Model      string          `json:"model"`
			StopReason string          `json:"stop_reason"`
			Content    json.RawMessage `json:"content"`
			Usage      *struct {
				InputTokens   *int64 `json:"input_tokens"`
				OutputTokens  *int64 `json:"output_tokens"`
				CacheRead     *int64 `json:"cache_read_input_tokens"`
				CacheCreation *int64 `json:"cache_creation_input_tokens"`
			} `json:"usage"`
		}
		if json.Unmarshal(raw, &body) != nil || len(body.Content) == 0 || body.Content[0] != '[' {
			return result, errors.New("the model response was not a usable message")
		}
		result.ID, result.Model, result.FinishReason = body.ID, body.Model, body.StopReason
		if u := body.Usage; u != nil && u.InputTokens != nil && u.OutputTokens != nil {
			// Anthropic reports cache reads and writes apart from input_tokens; the prompt is their sum.
			prompt := *u.InputTokens
			for _, extra := range []*int64{u.CacheRead, u.CacheCreation} {
				if extra != nil {
					prompt += *extra
				}
			}
			total := prompt + *u.OutputTokens
			result.Usage = Usage{PromptTokens: &prompt, CompletionTokens: u.OutputTokens, TotalTokens: &total, CachedTokens: u.CacheRead}
		}
	}
	if !result.Usage.valid() {
		return result, errors.New("the model reported impossible token counts")
	}
	return result, nil
}
