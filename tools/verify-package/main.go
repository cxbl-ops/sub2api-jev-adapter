package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	pluginv1 "github.com/cxbl-ops/sub2api-jev-adapter/sdk/pluginapi/v1"
	hclog "github.com/hashicorp/go-hclog"
	hcplugin "github.com/hashicorp/go-plugin"
)

type manifest struct {
	ID       string                 `json:"id"`
	Version  string                 `json:"version"`
	Runtimes map[string]runtimeSpec `json:"runtimes"`
	Files    map[string]string      `json:"files"`
}

type runtimeSpec struct {
	Path string `json:"path"`
}

type signature struct {
	Algorithm string `json:"algorithm"`
	KeyID     string `json:"key_id"`
	Signature string `json:"signature"`
}

func main() {
	packagePath := flag.String("package", "dist/sub2api-jev-adapter-0.1.0.s2plugin", "plugin package")
	publicKeyPath := flag.String("public-key", "dist/publisher-public-key.txt", "publisher public key")
	flag.Parse()

	publicKeyRaw, err := os.ReadFile(*publicKeyPath)
	check(err)
	publicKey, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(publicKeyRaw)))
	if err != nil || len(publicKey) != ed25519.PublicKeySize {
		panic("publisher public key is invalid")
	}

	archive, err := zip.OpenReader(*packagePath)
	check(err)
	defer func() { _ = archive.Close() }()
	entries := make(map[string]*zip.File, len(archive.File))
	for _, file := range archive.File {
		if !file.FileInfo().IsDir() {
			entries[filepath.ToSlash(file.Name)] = file
		}
	}
	manifestRaw := readEntry(entries["manifest.json"], 2<<20)
	signatureRaw := readEntry(entries["signature.json"], 64<<10)
	var packageManifest manifest
	check(json.Unmarshal(manifestRaw, &packageManifest))
	var packageSignature signature
	check(json.Unmarshal(signatureRaw, &packageSignature))
	if packageSignature.Algorithm != "ed25519" {
		panic("unsupported signature algorithm")
	}
	signatureBytes, err := base64.StdEncoding.DecodeString(packageSignature.Signature)
	check(err)
	if !ed25519.Verify(ed25519.PublicKey(publicKey), manifestRaw, signatureBytes) {
		panic("plugin signature verification failed")
	}
	for path, expected := range packageManifest.Files {
		data := readEntry(entries[path], 512<<20)
		digest := sha256.Sum256(data)
		if hex.EncodeToString(digest[:]) != expected {
			panic("file hash mismatch: " + path)
		}
	}

	runtimeKey := runtime.GOOS + "-" + runtime.GOARCH
	runtimeEntry, ok := packageManifest.Runtimes[runtimeKey]
	if !ok {
		panic("package has no runtime for " + runtimeKey)
	}
	runtimeData := readEntry(entries[runtimeEntry.Path], 512<<20)
	temporaryRoot, err := os.MkdirTemp("", "sub2api-jev-verify-")
	check(err)
	defer func() { _ = os.RemoveAll(temporaryRoot) }()
	binaryPath := filepath.Join(temporaryRoot, "plugin")
	check(os.WriteFile(binaryPath, runtimeData, 0o700))

	upstream := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/v1/decide" || request.Header.Get("Authorization") != "Bearer jv_test_not_a_real_key" {
			writer.WriteHeader(http.StatusUnauthorized)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"model":"jev-test","answers":{"ok":{"type":"noul","noul":1}},"usage":{"input_tokens":5,"cost_usd":0.00002,"credits_remaining_usd":99}}`)
	}))
	defer upstream.Close()
	certificatePath := filepath.Join(temporaryRoot, "test-ca.pem")
	certificatePEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: upstream.Certificate().Raw})
	check(os.WriteFile(certificatePath, certificatePEM, 0o600))

	checksum := sha256.Sum256(runtimeData)
	command := exec.Command(binaryPath)
	command.Env = append(os.Environ(), "SSL_CERT_FILE="+certificatePath, "NO_PROXY=*")
	client := hcplugin.NewClient(&hcplugin.ClientConfig{
		HandshakeConfig:  pluginv1.HandshakeConfig,
		Plugins:          pluginv1.ClientPluginMap(),
		Cmd:              command,
		AllowedProtocols: []hcplugin.Protocol{hcplugin.ProtocolGRPC},
		StartTimeout:     15 * time.Second,
		SecureConfig:     &hcplugin.SecureConfig{Checksum: checksum[:], Hash: sha256.New()},
		Logger:           hclog.NewNullLogger(),
		SyncStdout:       io.Discard,
		SyncStderr:       io.Discard,
	})
	defer client.Kill()
	rpcClient, err := client.Client()
	check(err)
	dispensed, err := rpcClient.Dispense(pluginv1.TransportPluginName)
	check(err)
	transport, ok := dispensed.(*pluginv1.TransportClient)
	if !ok || transport.TransportPluginClient == nil {
		panic("plugin transport is unavailable")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	info, err := transport.GetInfo(ctx, &pluginv1.GetInfoRequest{})
	check(err)
	configJSON := []byte(fmt.Sprintf(`{"api_key":"jv_test_not_a_real_key","base_url":%q}`, upstream.URL))
	validation, err := transport.ValidateConfig(ctx, &pluginv1.ValidateConfigRequest{ConfigJson: configJSON})
	if err != nil || !validation.Valid {
		panic(fmt.Sprintf("config validation failed: %v %v", validation, err))
	}
	applied, err := transport.ApplyConfig(ctx, &pluginv1.ApplyConfigRequest{ConfigJson: validation.NormalizedConfigJson})
	if err != nil || !applied.Applied {
		panic(fmt.Sprintf("config apply failed: %v %v", applied, err))
	}

	forward, err := transport.Forward(ctx)
	check(err)
	requestBody := []byte(`{"model":"jev-latest","stream":false,"state":"hello","questions":{"ok":{"type":"noul","instructions":"Is this hello?"}}}`)
	check(forward.Send(&pluginv1.ForwardRequest{Frame: &pluginv1.ForwardRequest_Start{Start: &pluginv1.ForwardRequestStart{
		RequestId: "verify", Method: http.MethodPost, Url: "https://example.invalid/v1/responses",
		Platform: "openai", AccountType: "oauth", ContentLength: int64(len(requestBody)), HasBody: true,
	}}}))
	check(forward.Send(&pluginv1.ForwardRequest{Frame: &pluginv1.ForwardRequest_BodyChunk{BodyChunk: requestBody}}))
	check(forward.Send(&pluginv1.ForwardRequest{Frame: &pluginv1.ForwardRequest_BodyEnd{BodyEnd: true}}))
	_ = forward.CloseSend()

	var responseBody bytes.Buffer
	var statusCode int32
	for {
		frame, receiveErr := forward.Recv()
		if errors.Is(receiveErr, io.EOF) {
			break
		}
		check(receiveErr)
		if transportErr := frame.GetError(); transportErr != nil {
			panic(transportErr.Message)
		}
		if start := frame.GetStart(); start != nil {
			statusCode = start.StatusCode
		}
		if chunk := frame.GetBodyChunk(); len(chunk) > 0 {
			_, _ = responseBody.Write(chunk)
		}
		if frame.GetEnd() != nil {
			break
		}
	}
	if statusCode != http.StatusOK ||
		!strings.Contains(responseBody.String(), `"model":"jev-test"`) ||
		strings.Contains(responseBody.String(), "credits_remaining") {
		panic(fmt.Sprintf("unexpected forward response: status=%d body=%s", statusCode, responseBody.String()))
	}
	if info.PluginId != packageManifest.ID || info.PluginVersion != packageManifest.Version {
		panic(fmt.Sprintf("runtime identity mismatch: %s %s", info.PluginId, info.PluginVersion))
	}
	fmt.Printf("verified: id=%s version=%s publisher=%s signature=trusted forward=ok\n",
		info.PluginId, info.PluginVersion, packageSignature.KeyID)
}

func readEntry(file *zip.File, limit int64) []byte {
	if file == nil {
		panic("required package entry is missing")
	}
	reader, err := file.Open()
	check(err)
	defer func() { _ = reader.Close() }()
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	check(err)
	if int64(len(data)) > limit {
		panic("package entry exceeds verification limit: " + file.Name)
	}
	return data
}

func check(err error) {
	if err != nil {
		panic(err)
	}
}
