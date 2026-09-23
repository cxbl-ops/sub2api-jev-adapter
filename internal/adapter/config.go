package adapter

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"
)

const (
	defaultBaseURL               = "https://jevtypesafeai.com"
	defaultRequestTimeoutSeconds = 120
	defaultHeaderTimeoutSeconds  = 90
	defaultMaxRequestBytes       = int64(64 << 20)
	defaultMaxResponseBytes      = int64(16 << 20)
	maxRequestBytes              = int64(256 << 20)
	maxResponseBytes             = int64(64 << 20)
)

type Config struct {
	APIKey                       string `json:"api_key"`
	BaseURL                      string `json:"base_url"`
	RequestTimeoutSeconds        int    `json:"request_timeout_seconds"`
	ResponseHeaderTimeoutSeconds int    `json:"response_header_timeout_seconds"`
	MaxRequestBytes              int64  `json:"max_request_bytes"`
	MaxResponseBytes             int64  `json:"max_response_bytes"`
	UseAccountProxy              bool   `json:"use_account_proxy"`
}

func defaultConfig() Config {
	return Config{
		BaseURL:                      defaultBaseURL,
		RequestTimeoutSeconds:        defaultRequestTimeoutSeconds,
		ResponseHeaderTimeoutSeconds: defaultHeaderTimeoutSeconds,
		MaxRequestBytes:              defaultMaxRequestBytes,
		MaxResponseBytes:             defaultMaxResponseBytes,
		UseAccountProxy:              true,
	}
}

func parseConfig(raw []byte) (Config, []byte, error) {
	cfg := defaultConfig()
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, nil, fmt.Errorf("配置 JSON 无效: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return Config{}, nil, errors.New("配置只能包含一个 JSON 对象")
	}
	cfg.APIKey = strings.TrimSpace(cfg.APIKey)
	cfg.BaseURL = strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if cfg.APIKey == "" {
		return Config{}, nil, errors.New("JEV API Key 不能为空")
	}
	if !strings.HasPrefix(cfg.APIKey, "jv_") {
		return Config{}, nil, errors.New("JEV API Key 格式无效")
	}
	parsed, err := url.Parse(cfg.BaseURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return Config{}, nil, errors.New("base_url 必须是无凭据、查询参数和片段的 HTTPS 地址")
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return Config{}, nil, errors.New("base_url 只能包含协议和主机，不能包含路径")
	}
	if cfg.RequestTimeoutSeconds < 5 || cfg.RequestTimeoutSeconds > 300 {
		return Config{}, nil, errors.New("request_timeout_seconds 必须在 5 到 300 之间")
	}
	if cfg.ResponseHeaderTimeoutSeconds < 5 || cfg.ResponseHeaderTimeoutSeconds > 300 {
		return Config{}, nil, errors.New("response_header_timeout_seconds 必须在 5 到 300 之间")
	}
	if cfg.MaxRequestBytes < 1<<20 || cfg.MaxRequestBytes > maxRequestBytes {
		return Config{}, nil, fmt.Errorf("max_request_bytes 必须在 %d 到 %d 之间", 1<<20, maxRequestBytes)
	}
	if cfg.MaxResponseBytes < 1<<20 || cfg.MaxResponseBytes > maxResponseBytes {
		return Config{}, nil, fmt.Errorf("max_response_bytes 必须在 %d 到 %d 之间", 1<<20, maxResponseBytes)
	}
	normalized, err := json.Marshal(cfg)
	if err != nil {
		return Config{}, nil, err
	}
	return cfg, normalized, nil
}

func (c Config) requestTimeout() time.Duration {
	return time.Duration(c.RequestTimeoutSeconds) * time.Second
}

func (c Config) responseHeaderTimeout() time.Duration {
	return time.Duration(c.ResponseHeaderTimeoutSeconds) * time.Second
}
