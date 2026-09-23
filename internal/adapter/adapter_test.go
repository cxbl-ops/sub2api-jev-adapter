package adapter

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	pluginv1 "github.com/cxbl-ops/sub2api-jev-adapter/sdk/pluginapi/v1"
)

func validConfig(baseURL string) Config {
	cfg := defaultConfig()
	cfg.APIKey = "jv_test_not_a_real_key"
	cfg.BaseURL = baseURL
	return cfg
}

func TestParseConfigNormalizesAndRejectsUnknownFields(t *testing.T) {
	cfg, normalized, err := parseConfig([]byte(`{"api_key":" jv_test_not_a_real_key ","base_url":"https://example.com/","request_timeout_seconds":30,"response_header_timeout_seconds":20,"max_request_bytes":1048576,"max_response_bytes":1048576,"use_account_proxy":false}`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.APIKey != "jv_test_not_a_real_key" || cfg.BaseURL != "https://example.com" {
		t.Fatalf("unexpected normalized config: %+v", cfg)
	}
	if !json.Valid(normalized) {
		t.Fatal("normalized config is invalid JSON")
	}
	if _, _, err := parseConfig([]byte(`{"api_key":"jv_test_not_a_real_key","unknown":true}`)); err == nil {
		t.Fatal("unknown field was accepted")
	}
}

func TestBuildJEVRequestBodyPreservesStructuredState(t *testing.T) {
	body, err := buildJEVRequestBody([]byte(`{"model":"jev-latest","state":{"ticket":"charged twice"},"questions":{"route":{"type":"choice","instructions":"Route?","criteria":{"billing":"money","bug":"broken"}}}}`), "jev-latest")
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if string(got["state"]) != `{"ticket":"charged twice"}` {
		t.Fatalf("state changed: %s", got["state"])
	}
}

func TestExecuteJEVWrapsResponseAndHidesRemainingCredits(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/decide" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer jv_test_not_a_real_key" {
			t.Fatalf("unexpected auth header")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"model":"jev-1.13.0","answers":{"ok":{"type":"noul","noul":1}},"usage":{"input_tokens":7,"cost_usd":0.00003,"credits_remaining_usd":99}}`)
	}))
	defer server.Close()

	p := New("0.1.0")
	cfg := validConfig(server.URL)
	start := &pluginv1.ForwardRequestStart{RequestId: "req-1"}
	request := []byte(`{"model":"jev-latest","stream":false,"state":"hello","questions":{"ok":{"type":"noul","instructions":"Is this hello?"}}}`)
	response, sent, err := p.executeJEV(context.Background(), cfg, start, request, "jev-latest", false)
	if err != nil || !sent {
		t.Fatalf("executeJEV: sent=%v err=%v", sent, err)
	}
	defer func() { _ = response.Body.Close() }()
	body, _ := io.ReadAll(response.Body)
	if !strings.Contains(string(body), `"model":"jev-1.13.0"`) || !strings.Contains(string(body), `"input_tokens":7`) {
		t.Fatalf("missing wrapped response: %s", body)
	}
	if strings.Contains(string(body), "credits_remaining") {
		t.Fatalf("remaining credits leaked: %s", body)
	}
}

func TestExecuteTransparentPreservesNonJEVTraffic(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if string(body) != `{"model":"gpt-test"}` || r.Header.Get("Authorization") != "Bearer original" {
			t.Fatalf("transparent request changed")
		}
		w.Header().Set("X-Upstream", "ok")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, "transparent")
	}))
	defer server.Close()
	p := New("0.1.0")
	cfg := validConfig("https://jevtypesafeai.com")
	start := &pluginv1.ForwardRequestStart{
		Method: http.MethodPost, Url: server.URL, Headers: map[string]*pluginv1.HeaderValues{
			"Authorization": {Values: []string{"Bearer original"}},
		},
	}
	response, sent, err := p.executeTransparent(context.Background(), cfg, start, []byte(`{"model":"gpt-test"}`))
	if err != nil || !sent {
		t.Fatalf("transparent execute: sent=%v err=%v", sent, err)
	}
	defer func() { _ = response.Body.Close() }()
	body, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusCreated || string(body) != "transparent" || response.Header.Get("X-Upstream") != "ok" {
		t.Fatalf("unexpected response: %d %q", response.StatusCode, body)
	}
}

func TestJEVFailureDoesNotExposeUpstreamBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, "secret echoed request")
	}))
	defer server.Close()
	p := New("0.1.0")
	request := []byte(`{"model":"jev-latest","state":"private","questions":{"ok":{"type":"noul","instructions":"test"}}}`)
	response, _, err := p.executeJEV(context.Background(), validConfig(server.URL), &pluginv1.ForwardRequestStart{}, request, "jev-latest", false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	body, _ := io.ReadAll(response.Body)
	if strings.Contains(string(body), "secret echoed") || strings.Contains(string(body), "private") {
		t.Fatalf("sensitive upstream response leaked: %s", body)
	}
}
