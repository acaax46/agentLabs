package execution

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type ModelClient struct {
	BaseURL string
	APIKey  string
	HTTP    *http.Client
}
type modelResponse struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int64 `json:"prompt_tokens"`
		CompletionTokens int64 `json:"completion_tokens"`
		TotalTokens      int64 `json:"total_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error,omitempty"`
}

func (c ModelClient) Complete(ctx context.Context, model, system, prompt string) (content, actualModel, responseID string, input, output int64, duration int64, err error) {
	if strings.TrimSpace(c.BaseURL) == "" || strings.TrimSpace(c.APIKey) == "" {
		return "", "", "", 0, 0, 0, fmt.Errorf("real model gateway is not configured")
	}
	base := strings.TrimRight(c.BaseURL, "/")
	endpoint := base
	if !strings.HasSuffix(base, "/chat/completions") && !strings.HasSuffix(base, "/responses") {
		if !strings.HasSuffix(base, "/v1") {
			base += "/v1"
		}
		endpoint = base + "/chat/completions"
	}
	payload := map[string]any{"model": model, "messages": []map[string]string{{"role": "system", "content": system}, {"role": "user", "content": prompt}}, "stream": false}
	payload["max_tokens"] = 512
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", "", "", 0, 0, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	req.Header.Set("Content-Type", "application/json")
	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Minute}
	}
	safeClient := *client
	safeClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	started := time.Now()
	resp, err := safeClient.Do(req)
	duration = time.Since(started).Milliseconds()
	if err != nil {
		return "", "", "", 0, 0, duration, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if readErr != nil {
		return "", "", "", 0, 0, duration, readErr
	}
	var result modelResponse
	if err = json.Unmarshal(raw, &result); err != nil {
		return "", "", "", 0, 0, duration, fmt.Errorf("model response invalid: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", "", result.ID, 0, 0, duration, fmt.Errorf("model gateway returned %s", resp.Status)
	}
	var evidence struct {
		Usage *struct {
			Input  *int64 `json:"prompt_tokens"`
			Output *int64 `json:"completion_tokens"`
		} `json:"usage"`
	}
	_ = json.Unmarshal(raw, &evidence)
	if evidence.Usage == nil || evidence.Usage.Input == nil || evidence.Usage.Output == nil || result.ID == "" {
		return "", "", result.ID, 0, 0, duration, fmt.Errorf("model response did not include final usage/request identity")
	}
	if len(result.Choices) == 0 {
		return "", "", result.ID, result.Usage.PromptTokens, result.Usage.CompletionTokens, duration, fmt.Errorf("model returned no choices")
	}
	return result.Choices[0].Message.Content, result.Model, result.ID, result.Usage.PromptTokens, result.Usage.CompletionTokens, duration, nil
}
