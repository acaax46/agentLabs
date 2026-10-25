package platform

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// publicReplyPrefix decodes only the published summary string while JSON is arriving. Tool
// arguments and reasoning fields never become a chat reply. An incomplete escape waits for its
// next chunk instead of displaying malformed JSON or a fake typewriter animation.
func publicReplyPrefix(raw string) string {
	start := strings.IndexByte(raw, '{')
	if start < 0 {
		return ""
	}
	raw = raw[start:]
	decoder := json.NewDecoder(strings.NewReader(raw))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return ""
	}
	var rest string
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return ""
		}
		if key == "summary" {
			rest = strings.TrimSpace(raw[decoder.InputOffset():])
			break
		}
		var discard json.RawMessage
		if decoder.Decode(&discard) != nil {
			return ""
		}
	}
	if !strings.HasPrefix(rest, ":") {
		return ""
	}
	rest = strings.TrimSpace(rest[1:])
	if !strings.HasPrefix(rest, `"`) {
		return ""
	}
	rest = rest[1:]
	escaped := false
	end := len(rest)
	for i, r := range rest {
		if escaped {
			escaped = false
			continue
		}
		if r == '\\' {
			escaped = true
			continue
		}
		if r == '"' {
			end = i
			break
		}
	}
	text := rest[:end]
	// Do not publish a replacement rune for the first half of an escaped surrogate pair.
	if len(text) >= 6 && strings.EqualFold(text[len(text)-6:len(text)-4], `\u`) {
		var unit uint16
		_, _ = fmt.Sscanf(text[len(text)-4:], "%x", &unit)
		if unit >= 0xd800 && unit <= 0xdbff {
			text = text[:len(text)-6]
		}
	}
	for trim := 0; trim <= 12 && len(text) > 0; trim++ {
		if utf8.ValidString(text) {
			var value string
			if err := json.Unmarshal([]byte(`"`+text+`"`), &value); err == nil {
				return value
			}
		}
		text = text[:len(text)-1]
	}
	return ""
}

func (x *execution) replyStream(ctx context.Context, step int) (func(string), func(string)) {
	var raw strings.Builder
	sent := ""
	last := time.Now()
	emit := func(force bool) {
		text := publicReplyPrefix(raw.String())
		if len(text) > 32<<10 {
			text = cutBytes(text, 32<<10)
		}
		if text == sent || (!force && len(text)-len(sent) < 96 && time.Since(last) < 150*time.Millisecond) {
			return
		}
		if strings.HasPrefix(text, sent) {
			x.event(ctx, "info", "assistant", "reply.delta", text[len(sent):], map[string]any{"step": step})
			sent = text
			last = time.Now()
		}
	}
	onText := func(chunk string) {
		if raw.Len()+len(chunk) <= 256<<10 {
			raw.WriteString(chunk)
			emit(false)
		}
	}
	finish := func(full string) {
		if raw.Len() == 0 {
			raw.WriteString(full)
		}
		emit(true)
	}
	return onText, finish
}
