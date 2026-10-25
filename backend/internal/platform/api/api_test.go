package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"agentlab.local/backend/internal/access"
)

type observedResponse struct {
	*httptest.ResponseRecorder
	written bool
}

func (w *observedResponse) WriteHeader(status int) {
	w.written = true
	w.ResponseRecorder.WriteHeader(status)
}

func (w *observedResponse) Write(body []byte) (int, error) {
	w.written = true
	return w.ResponseRecorder.Write(body)
}

func TestWriteErrorDistinguishesClientCancellationFromServerFailures(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	expired, done := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer done()
	for _, tc := range []struct {
		name    string
		ctx     context.Context
		err     error
		status  int
		code    string
		level   string
		message string
	}{
		{"client canceled", canceled, context.Canceled, 0, "", "DEBUG", "platform api request canceled"},
		{"database wraps client cancellation", canceled, fmt.Errorf("connection checkout: %w", context.Canceled), 0, "", "DEBUG", "platform api request canceled"},
		{"internal cancellation with live request", context.Background(), context.Canceled, 500, "INTERNAL_ERROR", "ERROR", "platform api internal error"},
		{"database failure", context.Background(), errors.New("database offline"), 500, "INTERNAL_ERROR", "ERROR", "platform api internal error"},
		{"database failure despite canceled request", canceled, errors.New("database offline"), 500, "INTERNAL_ERROR", "ERROR", "platform api internal error"},
		{"request deadline", expired, context.DeadlineExceeded, 500, "INTERNAL_ERROR", "ERROR", "platform api internal error"},
		{"authorization error", context.Background(), &access.Error{Code: "NOT_FOUND", Message: "资源不存在或无权访问。", Status: 404}, 404, "NOT_FOUND", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var logs bytes.Buffer
			previous := slog.Default()
			slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
			t.Cleanup(func() { slog.SetDefault(previous) })
			r := httptest.NewRequest(http.MethodGet, "/agents", nil).WithContext(tc.ctx)
			w := &observedResponse{ResponseRecorder: httptest.NewRecorder()}
			w.Header().Set("X-Request-ID", "cancel-check")
			writeError(w, r, tc.err)
			if tc.status == 0 {
				if w.written || w.Body.Len() != 0 {
					t.Fatalf("canceled request received a response: status=%d body=%s", w.Code, w.Body.String())
				}
			} else {
				var body errorBody
				if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
					t.Fatal(err)
				}
				if w.Code != tc.status || body.Code != tc.code || body.RequestID != "cancel-check" {
					t.Fatalf("status=%d body=%+v", w.Code, body)
				}
				if strings.Contains(w.Body.String(), tc.err.Error()) && tc.status >= 500 {
					t.Fatalf("internal error leaked into response: %s", w.Body.String())
				}
			}
			if tc.level == "" {
				if logs.Len() != 0 {
					t.Fatalf("unexpected log: %s", logs.String())
				}
				return
			}
			var record map[string]any
			if err := json.Unmarshal(logs.Bytes(), &record); err != nil {
				t.Fatal(err)
			}
			if record["level"] != tc.level || record["msg"] != tc.message || record["request_id"] != "cancel-check" {
				t.Fatalf("unexpected log: %s", logs.String())
			}
		})
	}
}
