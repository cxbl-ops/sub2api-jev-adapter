package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	pluginv1 "github.com/cxbl-ops/sub2api-jev-adapter/sdk/pluginapi/v1"
)

const (
	pluginID   = "com.cxbl.jev-adapter"
	capability = "openai.oauth.outbound_transport.v1"
	chunkSize  = 32 * 1024
)

type Plugin struct {
	pluginv1.UnimplementedTransportPluginServer

	version string
	mu      sync.RWMutex
	config  *Config
	clients map[string]*http.Client
}

func New(version string) *Plugin {
	return &Plugin{version: version, clients: make(map[string]*http.Client)}
}

func (p *Plugin) GetInfo(context.Context, *pluginv1.GetInfoRequest) (*pluginv1.GetInfoResponse, error) {
	return &pluginv1.GetInfoResponse{
		PluginId:            pluginID,
		PluginVersion:       p.version,
		ProtocolVersion:     pluginv1.ProtocolVersion,
		TransportApiVersion: pluginv1.TransportAPIVersion,
		Capabilities:        []string{capability},
	}, nil
}

func (p *Plugin) Health(context.Context, *pluginv1.HealthRequest) (*pluginv1.HealthResponse, error) {
	p.mu.RLock()
	cfg := p.config
	p.mu.RUnlock()
	status := map[string]any{"configured": cfg != nil, "adapter": "jev", "model_prefix": "jev-"}
	if cfg != nil {
		status["base_url"] = cfg.BaseURL
	}
	statusJSON, _ := json.Marshal(status)
	return &pluginv1.HealthResponse{
		Healthy:    true,
		Message:    "JEV Adapter 运行中",
		StatusJson: string(statusJSON),
	}, nil
}

func (p *Plugin) ValidateConfig(_ context.Context, req *pluginv1.ValidateConfigRequest) (*pluginv1.ValidateConfigResponse, error) {
	if req == nil {
		return &pluginv1.ValidateConfigResponse{Valid: false, Message: "配置为空"}, nil
	}
	_, normalized, err := parseConfig(req.ConfigJson)
	if err != nil {
		return &pluginv1.ValidateConfigResponse{Valid: false, Message: err.Error()}, nil
	}
	return &pluginv1.ValidateConfigResponse{Valid: true, Message: "配置有效", NormalizedConfigJson: normalized}, nil
}

func (p *Plugin) ApplyConfig(_ context.Context, req *pluginv1.ApplyConfigRequest) (*pluginv1.ApplyConfigResponse, error) {
	if req == nil {
		return &pluginv1.ApplyConfigResponse{Applied: false, Message: "配置为空"}, nil
	}
	cfg, _, err := parseConfig(req.ConfigJson)
	if err != nil {
		return &pluginv1.ApplyConfigResponse{Applied: false, Message: err.Error()}, nil
	}
	p.mu.Lock()
	oldClients := p.clients
	p.config = &cfg
	p.clients = make(map[string]*http.Client)
	p.mu.Unlock()
	closeIdleClients(oldClients)
	return &pluginv1.ApplyConfigResponse{Applied: true, Message: "配置已应用"}, nil
}

func (p *Plugin) TestConfig(ctx context.Context, req *pluginv1.TestConfigRequest) (*pluginv1.TestConfigResponse, error) {
	if req == nil {
		return &pluginv1.TestConfigResponse{Success: false, Message: "配置为空"}, nil
	}
	cfg, _, err := parseConfig(req.ConfigJson)
	if err != nil {
		return &pluginv1.TestConfigResponse{Success: false, Message: err.Error()}, nil
	}
	started := time.Now()
	probe := []byte(`{"model":"jev-latest","state":"Sub2API JEV plugin connectivity test","questions":{"reachable":{"type":"noul","instructions":"Is this a connectivity test?"}}}`)
	probeCtx, cancel := context.WithTimeout(ctx, cfg.requestTimeout())
	defer cancel()
	request, err := p.newJEVHTTPRequest(probeCtx, cfg, "", probe)
	if err != nil {
		return &pluginv1.TestConfigResponse{Success: false, Message: "无法创建测试请求"}, nil
	}
	client, err := p.clientFor(cfg, "")
	if err != nil {
		return &pluginv1.TestConfigResponse{Success: false, Message: "网络配置无效"}, nil
	}
	response, err := client.Do(request)
	if err != nil {
		return &pluginv1.TestConfigResponse{Success: false, Message: "JEV 连接失败", LatencyMs: time.Since(started).Milliseconds()}, nil
	}
	defer func() { _ = response.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, cfg.MaxResponseBytes))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return &pluginv1.TestConfigResponse{Success: false, Message: fmt.Sprintf("JEV 返回 HTTP %d", response.StatusCode), LatencyMs: time.Since(started).Milliseconds()}, nil
	}
	return &pluginv1.TestConfigResponse{Success: true, Message: "JEV Key 与网络连接正常（本测试会产生一次极小额 JEV 调用）", LatencyMs: time.Since(started).Milliseconds()}, nil
}

func (p *Plugin) Forward(stream pluginv1.TransportPlugin_ForwardServer) error {
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	start := first.GetStart()
	if start == nil || strings.TrimSpace(start.Method) == "" || strings.TrimSpace(start.Url) == "" {
		return sendPluginError(stream, "INVALID_REQUEST", "缺少请求元数据", false)
	}
	cfg, ok := p.configSnapshot()
	if !ok {
		return sendPluginError(stream, "NOT_CONFIGURED", "JEV 插件尚未配置", false)
	}
	if start.ContentLength > cfg.MaxRequestBytes {
		return sendPluginError(stream, "REQUEST_TOO_LARGE", "请求体超过插件配置上限", false)
	}
	body, err := receiveBody(stream, cfg.MaxRequestBytes)
	if err != nil {
		return sendPluginError(stream, "INVALID_REQUEST_BODY", err.Error(), false)
	}
	response, requestSent, err := p.execute(stream.Context(), cfg, start, body)
	if err != nil {
		return sendPluginError(stream, "TRANSPORT_ERROR", err.Error(), requestSent)
	}
	defer func() { _ = response.Body.Close() }()
	return sendHTTPResponse(stream, response)
}

func (p *Plugin) configSnapshot() (Config, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.config == nil {
		return Config{}, false
	}
	return *p.config, true
}

func receiveBody(stream pluginv1.TransportPlugin_ForwardServer, limit int64) ([]byte, error) {
	var body bytes.Buffer
	for {
		frame, err := stream.Recv()
		if err == io.EOF {
			return nil, errors.New("请求体未正常结束")
		}
		if err != nil {
			return nil, err
		}
		if chunk := frame.GetBodyChunk(); len(chunk) > 0 {
			if int64(body.Len())+int64(len(chunk)) > limit {
				return nil, errors.New("请求体超过插件配置上限")
			}
			_, _ = body.Write(chunk)
			continue
		}
		if frame.GetBodyEnd() {
			return body.Bytes(), nil
		}
		return nil, errors.New("请求帧顺序无效")
	}
}

func sendPluginError(stream pluginv1.TransportPlugin_ForwardServer, code, message string, requestSent bool) error {
	return stream.Send(&pluginv1.ForwardResponse{Frame: &pluginv1.ForwardResponse_Error{Error: &pluginv1.ForwardResponseError{
		Code: code, Message: message, RequestSent: requestSent,
	}}})
}

func sendHTTPResponse(stream pluginv1.TransportPlugin_ForwardServer, response *http.Response) error {
	if response == nil || response.Body == nil {
		return sendPluginError(stream, "INVALID_RESPONSE", "上游响应为空", true)
	}
	if err := stream.Send(&pluginv1.ForwardResponse{Frame: &pluginv1.ForwardResponse_Start{Start: &pluginv1.ForwardResponseStart{
		StatusCode:    int32(response.StatusCode),
		Status:        response.Status,
		Protocol:      response.Proto,
		ProtocolMajor: int32(response.ProtoMajor),
		ProtocolMinor: int32(response.ProtoMinor),
		Headers:       headersToPlugin(response.Header),
		ContentLength: response.ContentLength,
	}}}); err != nil {
		return err
	}
	buffer := make([]byte, chunkSize)
	var received int64
	started := time.Now()
	for {
		count, err := response.Body.Read(buffer)
		if count > 0 {
			received += int64(count)
			chunk := append([]byte(nil), buffer[:count]...)
			if sendErr := stream.Send(&pluginv1.ForwardResponse{Frame: &pluginv1.ForwardResponse_BodyChunk{BodyChunk: chunk}}); sendErr != nil {
				return sendErr
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return sendPluginError(stream, "RESPONSE_READ_ERROR", "读取上游响应失败", true)
		}
	}
	return stream.Send(&pluginv1.ForwardResponse{Frame: &pluginv1.ForwardResponse_End{End: &pluginv1.ForwardResponseEnd{
		BytesReceived: received,
		DurationMs:    time.Since(started).Milliseconds(),
	}}})
}

func headersToPlugin(header http.Header) map[string]*pluginv1.HeaderValues {
	out := make(map[string]*pluginv1.HeaderValues, len(header))
	for key, values := range header {
		out[key] = &pluginv1.HeaderValues{Values: append([]string(nil), values...)}
	}
	return out
}

func headersFromPlugin(headers map[string]*pluginv1.HeaderValues) http.Header {
	out := make(http.Header, len(headers))
	for key, values := range headers {
		if values != nil {
			out[key] = append([]string(nil), values.Values...)
		}
	}
	return out
}

func closeIdleClients(clients map[string]*http.Client) {
	for _, client := range clients {
		if client != nil {
			client.CloseIdleConnections()
		}
	}
}
