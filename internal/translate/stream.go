package translate

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

var (
	ErrModelMissing = errors.New("模型不可用，请在 magpie → Agents 里选一个")
	ErrAuth         = errors.New("magpie 拒绝了这个请求，检查 provider 登录状态")
	ErrInterrupted  = errors.New("连接中断")
	ErrUpstream     = errors.New("上游返回错误")
)

type Request struct {
	Model    string
	Target   string
	Text     string
	Glossary string
}

// Stream 向网关发起流式翻译，逐段回调 onDelta，返回完整译文。
// 流中断但已有增量时返回 (已收内容, ErrInterrupted)。
func Stream(ctx context.Context, base string, req Request, onDelta func(string)) (string, error) {
	body, err := json.Marshal(map[string]any{
		"model":       req.Model,
		"stream":      true,
		"temperature": 0,
		// magpie 上游默认开着思考，首字能到 30s；实测扁平 reasoning_effort=low 才有效。
		"reasoning_effort": "low",
		"messages": []map[string]string{
			{"role": "system", "content": SystemPrompt(req.Target)},
			{"role": "user", "content": UserPrompt(req.Text, req.Glossary)},
		},
	})
	if err != nil {
		return "", err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	if key := APIKey(); key != "" {
		httpReq.Header.Set("Authorization", "Bearer "+key)
	}

	resp, err := SharedClient.Do(httpReq)
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("%w: %v", ErrGatewayDown, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", statusError(resp.StatusCode, string(snippet))
	}

	var out strings.Builder
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		payload, ok := ssePayload(sc.Text())
		if !ok {
			continue
		}
		if payload == "[DONE]" {
			break
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal([]byte(payload), &chunk) != nil {
			continue
		}
		if chunk.Error != nil && chunk.Error.Message != "" {
			if out.Len() > 0 {
				return out.String(), ErrInterrupted
			}
			return "", fmt.Errorf("%w: %s", ErrUpstream, chunk.Error.Message)
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		// 只取 content；delta.reasoning_content 是思考过程，绝不能上屏。
		if delta := chunk.Choices[0].Delta.Content; delta != "" {
			out.WriteString(delta)
			if onDelta != nil {
				onDelta(delta)
			}
		}
	}
	if out.Len() > 0 {
		return out.String(), nil
	}
	if err := sc.Err(); err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("%w: %v", ErrUpstream, err)
	}
	return "", fmt.Errorf("%w: 没有返回内容", ErrUpstream)
}

// ssePayload 从一行 SSE 里取出 data 负载。magpie 发的是 "data:{json}"（冒号后没空格），
// 标准写法是 "data: {json}"，两种都要认。
func ssePayload(line string) (string, bool) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, ":") || strings.HasPrefix(line, "event:") {
		return "", false
	}
	if !strings.HasPrefix(line, "data:") {
		return "", false
	}
	return strings.TrimSpace(strings.TrimPrefix(line, "data:")), true
}

func statusError(code int, body string) error {
	snippet := strings.TrimSpace(body)
	if len(snippet) > 200 {
		snippet = snippet[:200]
	}
	switch code {
	case http.StatusNotFound:
		return ErrModelMissing
	case http.StatusUnauthorized, http.StatusForbidden:
		return ErrAuth
	}
	return fmt.Errorf("%w: HTTP %d %s", ErrUpstream, code, snippet)
}
