package adapter

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	pluginv1 "github.com/cxbl-ops/sub2api-jev-adapter/sdk/pluginapi/v1"
)

type jevUpstreamResponse struct {
	Model   string          `json:"model"`
	Answers json.RawMessage `json:"answers"`
	Usage   struct {
		InputTokens         int      `json:"input_tokens"`
		OutputTokens        int      `json:"output_tokens,omitempty"`
		CostUSD             *float64 `json:"cost_usd,omitempty"`
		CreditsRemainingUSD *float64 `json:"credits_remaining_usd,omitempty"`
	} `json:"usage"`
}

type jevPublicUsage struct {
	InputTokens  int      `json:"input_tokens"`
	OutputTokens int      `json:"output_tokens,omitempty"`
	CostUSD      *float64 `json:"cost_usd,omitempty"`
}

type jevPublicResponse struct {
	Model   string          `json:"model"`
	Answers json.RawMessage `json:"answers"`
	Usage   jevPublicUsage  `json:"usage"`
}

func detectJEVRequest(body []byte) (model string, stream bool, isJEV bool) {
	var envelope struct {
		Model  string `json:"model"`
		Stream bool   `json:"stream"`
	}
	if json.Unmarshal(body, &envelope) != nil {
		return "", false, false
	}
	model = strings.TrimSpace(envelope.Model)
	lower := strings.ToLower(model)
	return model, envelope.Stream, lower == "jev" || strings.HasPrefix(lower, "jev-")
}

func buildJEVRequestBody(body []byte, model string) ([]byte, error) {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, errors.New("请求不是有效 JSON")
	}
	state := envelope["state"]
	questions := envelope["questions"]
	if nestedRaw := envelope["jev"]; len(nestedRaw) > 0 {
		var nested map[string]json.RawMessage
		if json.Unmarshal(nestedRaw, &nested) == nil {
			if len(state) == 0 {
				state = nested["state"]
			}
			if len(questions) == 0 {
				questions = nested["questions"]
			}
		}
	}
	if len(state) == 0 {
		state = envelope["input"]
	}
	if len(questions) == 0 {
		questions = envelope["jev_questions"]
	}
	if len(state) == 0 || bytes.Equal(bytes.TrimSpace(state), []byte("null")) {
		return nil, errors.New("JEV 请求必须提供 state（缺省时使用 input）")
	}
	if !validJSONObject(questions) {
		return nil, errors.New("JEV 请求必须提供非空 questions 对象")
	}
	modelRaw, _ := json.Marshal(model)
	native := map[string]json.RawMessage{
		"model":     modelRaw,
		"state":     state,
		"questions": questions,
	}
	return json.Marshal(native)
}

func validJSONObject(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var object map[string]json.RawMessage
	return json.Unmarshal(raw, &object) == nil && len(object) > 0
}

func (p *Plugin) executeJEV(ctx context.Context, cfg Config, start *pluginv1.ForwardRequestStart, body []byte, model string, stream bool) (*http.Response, bool, error) {
	nativeBody, err := buildJEVRequestBody(body, model)
	if err != nil {
		return openAIErrorResponse(http.StatusBadRequest, "invalid_request_error", "jev_invalid_request", err.Error()), false, nil
	}
	proxyURL := ""
	if cfg.UseAccountProxy {
		proxyURL = start.ProxyUrl
	}
	requestCtx, cancel := context.WithTimeout(ctx, cfg.requestTimeout())
	defer cancel()
	request, err := p.newJEVHTTPRequest(requestCtx, cfg, start.RequestId, nativeBody)
	if err != nil {
		return nil, false, errors.New("无法创建 JEV 请求")
	}
	client, err := p.clientFor(cfg, proxyURL)
	if err != nil {
		return nil, false, err
	}
	response, err := client.Do(request)
	if err != nil {
		return openAIFailedResponse(model, stream, "jev_transport_error", "JEV 上游连接失败"), true, nil
	}
	defer func() { _ = response.Body.Close() }()
	upstreamBody, readErr := io.ReadAll(io.LimitReader(response.Body, cfg.MaxResponseBytes+1))
	if readErr != nil {
		return openAIFailedResponse(model, stream, "jev_response_error", "读取 JEV 响应失败"), true, nil
	}
	if int64(len(upstreamBody)) > cfg.MaxResponseBytes {
		return openAIFailedResponse(model, stream, "jev_response_too_large", "JEV 响应超过插件配置上限"), true, nil
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return openAIFailedResponse(model, stream, jevHTTPErrorCode(response.StatusCode), jevHTTPErrorMessage(response.StatusCode)), true, nil
	}
	var native jevUpstreamResponse
	if err := json.Unmarshal(upstreamBody, &native); err != nil || strings.TrimSpace(native.Model) == "" || !validJSONObject(native.Answers) {
		return openAIFailedResponse(model, stream, "jev_invalid_response", "JEV 返回了无效响应"), true, nil
	}
	public := jevPublicResponse{
		Model:   native.Model,
		Answers: native.Answers,
		Usage: jevPublicUsage{
			InputTokens: native.Usage.InputTokens, OutputTokens: native.Usage.OutputTokens, CostUSD: native.Usage.CostUSD,
		},
	}
	publicJSON, err := json.Marshal(public)
	if err != nil {
		return openAIFailedResponse(model, stream, "jev_invalid_response", "无法编码 JEV 响应"), true, nil
	}
	return openAISuccessResponse(native.Model, public, publicJSON, stream), true, nil
}

func (p *Plugin) newJEVHTTPRequest(ctx context.Context, cfg Config, requestID string, body []byte) (*http.Request, error) {
	base, err := url.Parse(cfg.BaseURL)
	if err != nil {
		return nil, err
	}
	base.Path = "/api/v1/decide"
	base.RawPath = ""
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, base.String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "sub2api-jev-adapter/0.1")
	if requestID = strings.TrimSpace(requestID); requestID != "" {
		request.Header.Set("X-Request-ID", requestID)
	}
	return request, nil
}

func openAISuccessResponse(model string, public jevPublicResponse, publicJSON []byte, stream bool) *http.Response {
	id := "resp_jev_" + randomID()
	messageID := "msg_jev_" + randomID()
	response := map[string]any{
		"id":         id,
		"object":     "response",
		"created_at": time.Now().Unix(),
		"status":     "completed",
		"model":      model,
		"output": []any{
			map[string]any{
				"id": messageID, "type": "message", "status": "completed", "role": "assistant",
				"content": []any{
					map[string]any{"type": "output_text", "text": string(publicJSON), "annotations": []any{}},
				},
			},
		},
		"usage": map[string]any{
			"input_tokens": public.Usage.InputTokens, "output_tokens": public.Usage.OutputTokens,
			"total_tokens":          public.Usage.InputTokens + public.Usage.OutputTokens,
			"input_tokens_details":  map[string]any{"cached_tokens": 0},
			"output_tokens_details": map[string]any{"reasoning_tokens": 0},
		},
		"jev": public,
	}
	encoded, _ := json.Marshal(response)
	if !stream {
		return memoryResponse(http.StatusOK, "application/json; charset=utf-8", encoded)
	}
	created := cloneMap(response)
	created["status"] = "in_progress"
	created["output"] = []any{}
	createdJSON, _ := json.Marshal(map[string]any{"type": "response.created", "response": created})
	deltaJSON, _ := json.Marshal(map[string]any{
		"type": "response.output_text.delta", "response_id": id, "item_id": messageID,
		"output_index": 0, "content_index": 0, "delta": string(publicJSON),
	})
	completedJSON, _ := json.Marshal(map[string]any{"type": "response.completed", "response": response})
	sse := appendSSE(nil, "response.created", createdJSON)
	sse = appendSSE(sse, "response.output_text.delta", deltaJSON)
	sse = appendSSE(sse, "response.completed", completedJSON)
	return memoryResponse(http.StatusOK, "text/event-stream; charset=utf-8", sse)
}

func openAIFailedResponse(model string, stream bool, code, message string) *http.Response {
	response := map[string]any{
		"id": "resp_jev_" + randomID(), "object": "response", "created_at": time.Now().Unix(),
		"status": "failed", "model": model, "output": []any{},
		"error": map[string]any{"type": "jev_error", "code": code, "message": message},
		"usage": map[string]any{"input_tokens": 0, "output_tokens": 0, "total_tokens": 0},
	}
	encoded, _ := json.Marshal(response)
	if !stream {
		return memoryResponse(http.StatusOK, "application/json; charset=utf-8", encoded)
	}
	event, _ := json.Marshal(map[string]any{"type": "response.failed", "response": response})
	return memoryResponse(http.StatusOK, "text/event-stream; charset=utf-8", appendSSE(nil, "response.failed", event))
}

func openAIErrorResponse(status int, errorType, code, message string) *http.Response {
	body, _ := json.Marshal(map[string]any{"error": map[string]any{
		"type": errorType, "code": code, "message": message,
	}})
	return memoryResponse(status, "application/json; charset=utf-8", body)
}

func memoryResponse(status int, contentType string, body []byte) *http.Response {
	return &http.Response{
		StatusCode: status, Status: fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1,
		Header: http.Header{"Content-Type": []string{contentType}},
		Body:   io.NopCloser(bytes.NewReader(body)), ContentLength: int64(len(body)),
	}
}

func appendSSE(dst []byte, event string, data []byte) []byte {
	dst = append(dst, "event: "...)
	dst = append(dst, event...)
	dst = append(dst, '\n')
	dst = append(dst, "data: "...)
	dst = append(dst, data...)
	dst = append(dst, '\n', '\n')
	return dst
}

func cloneMap(source map[string]any) map[string]any {
	out := make(map[string]any, len(source))
	for key, value := range source {
		out[key] = value
	}
	return out
}

func randomID() string {
	raw := make([]byte, 12)
	if _, err := rand.Read(raw); err != nil {
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(raw)
}

func jevHTTPErrorCode(status int) string {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return "jev_credentials_invalid"
	case http.StatusPaymentRequired:
		return "jev_insufficient_credits"
	case http.StatusTooManyRequests:
		return "jev_rate_limited"
	case http.StatusBadRequest:
		return "jev_request_rejected"
	default:
		return "jev_upstream_error"
	}
}

func jevHTTPErrorMessage(status int) string {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return "JEV 插件凭据无效或账号不可用"
	case http.StatusPaymentRequired:
		return "JEV 上游余额不足"
	case http.StatusTooManyRequests:
		return "JEV 上游请求过于频繁"
	case http.StatusBadRequest:
		return "JEV 拒绝了请求，请检查 state 和 questions"
	default:
		return fmt.Sprintf("JEV 上游返回 HTTP %d", status)
	}
}
