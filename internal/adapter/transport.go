package adapter

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	pluginv1 "github.com/cxbl-ops/sub2api-jev-adapter/sdk/pluginapi/v1"
	xproxy "golang.org/x/net/proxy"
)

var hopByHopHeaders = map[string]struct{}{
	"connection":          {},
	"proxy-connection":    {},
	"keep-alive":          {},
	"proxy-authenticate":  {},
	"proxy-authorization": {},
	"te":                  {},
	"trailer":             {},
	"transfer-encoding":   {},
	"upgrade":             {},
}

func (p *Plugin) execute(ctx context.Context, cfg Config, start *pluginv1.ForwardRequestStart, body []byte) (*http.Response, bool, error) {
	model, stream, isJEV := detectJEVRequest(body)
	if isJEV {
		return p.executeJEV(ctx, cfg, start, body, model, stream)
	}
	return p.executeTransparent(ctx, cfg, start, body)
}

func (p *Plugin) executeTransparent(ctx context.Context, cfg Config, start *pluginv1.ForwardRequestStart, body []byte) (*http.Response, bool, error) {
	target, err := url.Parse(start.Url)
	if err != nil || (target.Scheme != "https" && target.Scheme != "http") || target.Host == "" {
		return nil, false, errors.New("原始上游 URL 无效")
	}
	request, err := http.NewRequestWithContext(ctx, start.Method, target.String(), bytes.NewReader(body))
	if err != nil {
		return nil, false, errors.New("无法创建透明转发请求")
	}
	request.Header = headersFromPlugin(start.Headers)
	stripHopByHop(request.Header)
	request.Host = start.Host
	request.ContentLength = int64(len(body))
	client, err := p.clientFor(cfg, start.ProxyUrl)
	if err != nil {
		return nil, false, err
	}
	response, err := client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, true, ctx.Err()
		}
		return nil, true, errors.New("透明转发上游连接失败")
	}
	stripHopByHop(response.Header)
	return response, true, nil
}

func (p *Plugin) clientFor(cfg Config, rawProxy string) (*http.Client, error) {
	rawProxy = strings.TrimSpace(rawProxy)
	key := fmt.Sprintf("%s|%d", rawProxy, cfg.ResponseHeaderTimeoutSeconds)
	p.mu.RLock()
	client := p.clients[key]
	p.mu.RUnlock()
	if client != nil {
		return client, nil
	}
	transport, err := newTransport(rawProxy, cfg.responseHeaderTimeout())
	if err != nil {
		return nil, err
	}
	client = &http.Client{
		Transport: transport,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	p.mu.Lock()
	if existing := p.clients[key]; existing != nil {
		p.mu.Unlock()
		transport.CloseIdleConnections()
		return existing, nil
	}
	p.clients[key] = client
	p.mu.Unlock()
	return client, nil
}

func newTransport(rawProxy string, responseHeaderTimeout time.Duration) (*http.Transport, error) {
	dialer := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           dialer.DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          256,
		MaxIdleConnsPerHost:   64,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: responseHeaderTimeout,
		ExpectContinueTimeout: time.Second,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
	}
	if strings.TrimSpace(rawProxy) == "" {
		return transport, nil
	}
	proxyURL, err := url.Parse(rawProxy)
	if err != nil || proxyURL.Host == "" {
		return nil, errors.New("账号代理 URL 无效")
	}
	switch strings.ToLower(proxyURL.Scheme) {
	case "http", "https":
		transport.Proxy = http.ProxyURL(proxyURL)
	case "socks5", "socks5h":
		var auth *xproxy.Auth
		if proxyURL.User != nil {
			password, _ := proxyURL.User.Password()
			auth = &xproxy.Auth{User: proxyURL.User.Username(), Password: password}
		}
		socksDialer, dialErr := xproxy.SOCKS5("tcp", proxyURL.Host, auth, dialer)
		if dialErr != nil {
			return nil, errors.New("无法初始化 SOCKS5 代理")
		}
		transport.Proxy = nil
		transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
			type contextDialer interface {
				DialContext(context.Context, string, string) (net.Conn, error)
			}
			if contextual, ok := socksDialer.(contextDialer); ok {
				return contextual.DialContext(ctx, network, address)
			}
			return socksDialer.Dial(network, address)
		}
	default:
		return nil, errors.New("账号代理仅支持 HTTP、HTTPS 或 SOCKS5")
	}
	return transport, nil
}

func stripHopByHop(header http.Header) {
	if header == nil {
		return
	}
	for _, token := range strings.Split(header.Get("Connection"), ",") {
		if token = strings.TrimSpace(token); token != "" {
			header.Del(token)
		}
	}
	for key := range header {
		if _, blocked := hopByHopHeaders[strings.ToLower(key)]; blocked {
			header.Del(key)
		}
	}
}
