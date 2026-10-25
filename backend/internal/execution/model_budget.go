package execution

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"agentlab.local/backend/internal/learning"
	"agentlab.local/backend/internal/usage"
)

type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}
type BudgetedModelResult struct {
	Content, Model, ResponseID string
	Usage                      usage.Usage
	DurationMS                 int64
	Sent                       bool
	Complete                   bool
}

// configuredEndpoint accepts only the text chat protocol implemented here.
func (c ModelClient) configuredEndpoint() (string, error) {
	u, err := url.Parse(strings.TrimSpace(c.BaseURL))
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.TrimSpace(c.APIKey) == "" {
		return "", fmt.Errorf("模型网关尚未正确配置。")
	}
	if u.Scheme != "https" && (u.Scheme != "http" || (u.Hostname() != "localhost" && !net.ParseIP(u.Hostname()).IsLoopback())) {
		return "", fmt.Errorf("模型网关需要 HTTPS 或本机测试地址。")
	}
	base := strings.TrimRight(u.String(), "/")
	if strings.HasSuffix(base, "/responses") {
		return "", fmt.Errorf("此连接尚未启用 Responses 协议，请使用 Chat Completions 地址。")
	}
	if !strings.HasSuffix(base, "/chat/completions") {
		if !strings.HasSuffix(base, "/v1") {
			base += "/v1"
		}
		base += "/chat/completions"
	}
	return base, nil
}
func inputBound(messages []ChatMessage) (int64, error) {
	var size int64 = 4096
	for _, m := range messages {
		if m.Role != "system" && m.Role != "user" && m.Role != "assistant" {
			return 0, fmt.Errorf("对话角色不受支持。")
		}
		size += int64(len([]byte(m.Content))) + 64
	}
	if size > 128*1024 {
		return 0, fmt.Errorf("当前项目和对话过长，请减少输入。")
	}
	return size, nil
}
func modelOutputBound(price learning.Price, messages []ChatMessage) (int, error) {
	input, err := inputBound(messages)
	if err != nil {
		return 0, err
	}
	rate := usage.Price{Version: price.ID, Currency: price.Currency, Input: price.Input, CachedRead: price.CachedRead, CacheWrite: price.CacheWrite, Output: price.Output}
	// Cached dimensions may be more expensive than normal input: reserve the worst input rate.
	rate.Input = maxRate(price.Input, price.CachedRead, price.CacheWrite)
	affordable := func(output int64) bool {
		_, err := rate.Cost(usage.Usage{Source: "provider-reported", InputTokens: &input, OutputTokens: &output})
		if err != nil {
			return false
		}
		cost, _ := rate.Cost(usage.Usage{Source: "provider-reported", InputTokens: &input, OutputTokens: &output})
		return cost <= price.Maximum
	}
	low, high := 0, 4096
	for low < high {
		mid := (low + high + 1) / 2
		if affordable(int64(mid)) {
			low = mid
		} else {
			high = mid - 1
		}
	}
	if low < 1 {
		return 0, fmt.Errorf("单次预占不足以覆盖输入上界与输出，请调整预算或缩小上下文。")
	}
	return low, nil
}
func maxRate(v ...int64) int64 {
	var m int64
	for _, n := range v {
		if n > m {
			m = n
		}
	}
	return m
}

type chatUsage struct {
	Input        *int64 `json:"prompt_tokens"`
	Output       *int64 `json:"completion_tokens"`
	Total        *int64 `json:"total_tokens"`
	InputDetails *struct {
		Cached *int64 `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
	OutputDetails *struct {
		Reasoning *int64 `json:"reasoning_tokens"`
	} `json:"completion_tokens_details"`
}
type budgetedChatResponse struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		Message      ChatMessage `json:"message"`
		FinishReason string      `json:"finish_reason"`
	} `json:"choices"`
	Usage *chatUsage `json:"usage"`
}

// CompleteBudgeted performs exactly one text request. beforeSend persists dispatch before client.Do.
// No transport errors or 429/5xx cause an automatic retry.
func (c ModelClient) CompleteBudgeted(ctx context.Context, price learning.Price, messages []ChatMessage, beforeSend func() error) (BudgetedModelResult, error) {
	result := BudgetedModelResult{Usage: usage.Usage{Source: "unknown"}}
	endpoint, err := c.configuredEndpoint()
	if err != nil {
		return result, err
	}
	maxOutput, err := modelOutputBound(price, messages)
	if err != nil {
		return result, err
	}
	payload := map[string]any{"model": price.Model, "messages": messages, "stream": false, "max_tokens": maxOutput, "n": 1}
	body, err := json.Marshal(payload)
	if err != nil {
		return result, err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(body))
	if err != nil {
		return result, fmt.Errorf("无法创建模型请求。")
	}
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	req.Header.Set("Content-Type", "application/json")
	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Minute}
	}
	safeClient := *client
	safeClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if err = beforeSend(); err != nil {
		return result, err
	}
	result.Sent = true
	started := time.Now()
	resp, err := safeClient.Do(req)
	result.DurationMS = time.Since(started).Milliseconds()
	if err != nil {
		return result, fmt.Errorf("模型请求结果未知，请核对原请求，未自动重发。")
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (2<<20)+1))
	if err != nil || len(raw) > 2<<20 {
		return result, fmt.Errorf("模型返回未能完整读取，等待核对。")
	}
	var value budgetedChatResponse
	if err = json.Unmarshal(raw, &value); err != nil {
		return result, fmt.Errorf("模型响应格式不完整，等待核对。")
	}
	result.ResponseID = value.ID
	result.Model = value.Model
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return result, fmt.Errorf("模型网关返回状态 %d，未自动重发。", resp.StatusCode)
	}
	if value.Usage != nil {
		u := value.Usage
		result.Usage = usage.Usage{Source: "provider-reported", InputTokens: u.Input, OutputTokens: u.Output, TotalTokens: u.Total}
		if u.InputDetails != nil {
			result.Usage.CachedInputTokens = u.InputDetails.Cached
		}
		if u.OutputDetails != nil {
			result.Usage.ReasoningTokens = u.OutputDetails.Reasoning
		}
	}
	if len(value.Choices) != 1 {
		return result, fmt.Errorf("模型没有返回唯一文本结果。")
	}
	result.Content = value.Choices[0].Message.Content
	if value.Model != price.Model {
		return result, fmt.Errorf("供应商返回模型 %q 与锁定价格模型 %q 不一致，需要核对后结算。", value.Model, price.Model)
	}
	if value.ID == "" || value.Usage == nil || value.Usage.Input == nil || value.Usage.Output == nil {
		return result, errors.New("供应商未返回完整的请求编号或最终用量，保留预占等待核对。")
	}
	rate := usage.Price{Version: price.ID, Currency: price.Currency, Input: price.Input, CachedRead: price.CachedRead, CacheWrite: price.CacheWrite, Output: price.Output}
	if _, err = rate.Cost(result.Usage); err != nil {
		return result, errors.New("供应商用量无法按当前价格结算，等待核对。")
	}
	result.Complete = true
	return result, nil
}
