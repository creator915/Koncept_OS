package transport

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"strings"
	"time"

)

type Config struct {
	BaseURL  string
	Model    string
	APIKey   string
	Thinking bool // emit DeepSeek thinking-mode fields (reasoning_effort + thinking)
}

func DefaultDeepSeekConfig(apiKey string) Config {
	return Config{
		BaseURL:  "https://api.deepseek.com",
		Model:    "deepseek-v4-pro",
		APIKey:   apiKey,
		Thinking: true,
	}
}

type StreamHandler struct {
	OnContent   func(string)
	OnReasoning func(string)
}

type Client struct {
	cfg  Config
	http *http.Client
}

func NewClient(cfg Config) *Client {
	return &Client{
		cfg:  cfg,
		http: &http.Client{Timeout: 10 * time.Minute},
	}
}

type chatRequest struct {
	Model           string          `json:"model"`
	Messages        []Message  `json:"messages"`
	Tools           []ToolSpec `json:"tools,omitempty"`
	Stream          bool            `json:"stream"`
	ReasoningEffort string          `json:"reasoning_effort,omitempty"`
	Thinking        *thinking       `json:"thinking,omitempty"`
}

type thinking struct {
	Type string `json:"type"`
}

type toolCallDelta struct {
	Index    int    `json:"index"`
	ID       string `json:"id,omitempty"`
	Type     string `json:"type,omitempty"`
	Function struct {
		Name      string `json:"name,omitempty"`
		Arguments string `json:"arguments,omitempty"`
	} `json:"function"`
}

type chatChunk struct {
	Choices []struct {
		Delta struct {
			Role             string          `json:"role,omitempty"`
			Content          string          `json:"content,omitempty"`
			ReasoningContent string          `json:"reasoning_content,omitempty"`
			ToolCalls        []toolCallDelta `json:"tool_calls,omitempty"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason,omitempty"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// errRetryable wraps any error the retry loop should treat as transient.
// Pre-stream HTTP errors and HTTP 5xx / 429 responses are wrapped before
// being returned from chatOnce. Stream errors (after status 200) are NOT
// wrapped — by then the handler may have already echoed partial content
// to the user, and a retry would double-emit.
var errRetryable = errors.New("retryable")

const (
	maxAttempts    = 3
	baseBackoff    = 400 * time.Millisecond // first retry waits ~400ms-600ms
	maxBackoffStep = 8 * time.Second        // cap per-step delay
	// streamInactivityTimeout caps how long we wait for the NEXT chunk
	// in an open stream. Pre-2026-05-20: stream read had no per-chunk
	// timeout — only the 10-minute total HTTP deadline. DeepSeek can
	// stall mid-stream silently (the 2026-05-20 pong batch #3 pong-05
	// saw 8 minutes of silence on one confirm_object call), and the
	// client just waited. 90s is a generous bound that catches stalls
	// while tolerating slow reasoning bursts on big prompts.
	streamInactivityTimeout = 90 * time.Second
)

// Chat sends a chat-completions request and streams the response, retrying
// transient failures (network errors, HTTP 5xx, HTTP 429) up to 3 attempts
// with exponential backoff + jitter. Non-retryable errors (4xx other than
// 429, malformed responses, mid-stream errors) bubble up immediately.
func (c *Client) Chat(ctx context.Context, messages []Message, tools []ToolSpec, h StreamHandler) (*Message, error) {
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		msg, err := c.chatOnce(ctx, messages, tools, h)
		if err == nil {
			return msg, nil
		}
		if !errors.Is(err, errRetryable) {
			return nil, err
		}
		lastErr = err
		if attempt == maxAttempts {
			break
		}
		delay := backoffDelay(attempt)
		fmt.Fprintf(os.Stderr, "[llm retry %d/%d after %v: %v]\n", attempt, maxAttempts-1, delay, err)
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return nil, fmt.Errorf("after %d attempts: %w", maxAttempts, lastErr)
}

// backoffDelay returns the wait time before the (attempt+1)-th try.
// Exponential up to maxBackoffStep, with ±25% jitter.
func backoffDelay(attempt int) time.Duration {
	step := baseBackoff << (attempt - 1)
	if step > maxBackoffStep {
		step = maxBackoffStep
	}
	jitter := time.Duration(rand.Int63n(int64(step / 2)))
	return step + jitter
}

func (c *Client) chatOnce(ctx context.Context, messages []Message, tools []ToolSpec, h StreamHandler) (*Message, error) {
	reqBody := chatRequest{
		Model:    c.cfg.Model,
		Messages: messages,
		Tools:    tools,
		Stream:   true,
	}
	if c.cfg.Thinking {
		reqBody.ReasoningEffort = "high"
		reqBody.Thinking = &thinking{Type: "enabled"}
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		// Marshal of our own structs cannot realistically fail; not retryable.
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.cfg.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

	resp, err := c.http.Do(req)
	if err != nil {
		// Network / transport error before we got headers — safe to retry.
		return nil, fmt.Errorf("%w: http: %v", errRetryable, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		// 5xx or 429 → server side / rate limited, retry.
		// 4xx (other) → caller error, no point retrying.
		if resp.StatusCode >= 500 || resp.StatusCode == 429 {
			return nil, fmt.Errorf("%w: llm http %d: %s", errRetryable, resp.StatusCode, b)
		}
		return nil, fmt.Errorf("llm http %d: %s", resp.StatusCode, b)
	}

	// From here on we may emit deltas to the handler. v9.0.3 nuance: a
	// stream error that fires BEFORE any delta has reached the handler
	// is still safe to retry — nothing the user saw will repeat. The
	// 2026-05-11 Terraria batch died here repeatedly: 5/5 instances hit
	// `context deadline exceeded` or `unexpected EOF` during the stream
	// before producing any visible response, and the pre-v9.0.3 code
	// surfaced that as a fatal non-retryable error, taking the whole
	// kcpos chat process down. Now: track whether any content has been
	// emitted; if not, wrap the stream error as retryable so the outer
	// Chat loop's backoff fires.
	emittedAny := false
	msg := &Message{Role: "assistant"}

	// Inactivity watchdog: cancels the response body if no chunk
	// arrives within streamInactivityTimeout. We track lastActivity
	// via an atomic-ish pointer that the scanner updates after every
	// successful read; the watchdog goroutine wakes periodically and
	// closes resp.Body (forcing scanner.Scan() to return false with
	// an error) if the gap exceeds the cap. The error then becomes
	// "errRetryable: stream inactivity" pre-emit or "read stream"
	// post-emit per the existing emitted-any policy.
	lastActivity := time.Now()
	var inactivityFired bool
	stopWatchdog := make(chan struct{})
	go func() {
		t := time.NewTicker(streamInactivityTimeout / 3)
		defer t.Stop()
		for {
			select {
			case <-stopWatchdog:
				return
			case <-t.C:
				if time.Since(lastActivity) > streamInactivityTimeout {
					inactivityFired = true
					_ = resp.Body.Close()
					return
				}
			}
		}
	}()
	defer close(stopWatchdog)

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		lastActivity = time.Now()
		line := scanner.Text()
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}
		var chunk chatChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return nil, fmt.Errorf("decode chunk: %w (line=%s)", err, line)
		}
		if chunk.Error != nil {
			return nil, fmt.Errorf("llm stream error: %s", chunk.Error.Message)
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		delta := chunk.Choices[0].Delta
		if delta.Content != "" {
			msg.Content += delta.Content
			emittedAny = true
			if h.OnContent != nil {
				h.OnContent(delta.Content)
			}
		}
		if delta.ReasoningContent != "" {
			msg.ReasoningContent += delta.ReasoningContent
			emittedAny = true
			if h.OnReasoning != nil {
				h.OnReasoning(delta.ReasoningContent)
			}
		}
		for _, tcd := range delta.ToolCalls {
			emittedAny = true
			for len(msg.ToolCalls) <= tcd.Index {
				msg.ToolCalls = append(msg.ToolCalls, ToolCall{Type: "function"})
			}
			tc := &msg.ToolCalls[tcd.Index]
			if tcd.ID != "" {
				tc.ID = tcd.ID
			}
			if tcd.Type != "" {
				tc.Type = tcd.Type
			}
			if tcd.Function.Name != "" {
				tc.Function.Name = tcd.Function.Name
			}
			if tcd.Function.Arguments != "" {
				tc.Function.Arguments += tcd.Function.Arguments
			}
		}
	}
	if err := scanner.Err(); err != nil {
		// v9.0.3: stream error before any delta reached the handler is
		// safe to retry — the user hasn't seen anything to repeat.
		// Common causes: server-side TCP close (`unexpected EOF`),
		// client HTTP body deadline (`context deadline exceeded`),
		// or — 2026-05-20 — inactivity watchdog forced-close.
		if !emittedAny {
			if inactivityFired {
				return nil, fmt.Errorf("%w: stream inactivity (no chunk for >%v)", errRetryable, streamInactivityTimeout)
			}
			return nil, fmt.Errorf("%w: read stream (no partial emitted): %v", errRetryable, err)
		}
		if inactivityFired {
			return nil, fmt.Errorf("read stream: inactivity timeout >%v (partial content already emitted; not retried — re-invoke or resume manually)", streamInactivityTimeout)
		}
		return nil, fmt.Errorf("read stream: %w (partial content already emitted; not retried — re-invoke or resume manually)", err)
	}
	// Treat inactivity-triggered close as an error even when scanner
	// has no Err (some buffered modes swallow EOF). Without partial
	// emit it's retryable.
	if inactivityFired {
		if !emittedAny {
			return nil, fmt.Errorf("%w: stream inactivity (no chunk for >%v)", errRetryable, streamInactivityTimeout)
		}
		return nil, fmt.Errorf("read stream: inactivity timeout >%v (partial content already emitted; not retried — re-invoke or resume manually)", streamInactivityTimeout)
	}
	return msg, nil
}
