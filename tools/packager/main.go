package main

import (
	"archive/zip"
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type manifest struct {
	SchemaVersion int                    `json:"schema_version"`
	ID            string                 `json:"id"`
	Name          string                 `json:"name"`
	Version       string                 `json:"version"`
	Description   string                 `json:"description,omitempty"`
	Author        string                 `json:"author,omitempty"`
	Requires      map[string]any         `json:"requires"`
	Capabilities  []map[string]string    `json:"capabilities"`
	Runtimes      map[string]runtimeSpec `json:"runtimes"`
	UI            map[string]string      `json:"ui"`
	Files         map[string]string      `json:"files"`
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
	root := flag.String("root", ".", "plugin project root")
	binary := flag.String("binary", "build/jev-adapter", "runtime binary")
	ui := flag.String("ui", "ui/index.html", "UI entrypoint")
	privateKeyPath := flag.String("private-key", "build/keys/publisher.private", "base64 Ed25519 private key")
	keyID := flag.String("key-id", "cxbl-jev-adapter-v1", "publisher key id")
	output := flag.String("output", "dist/sub2api-jev-adapter-0.1.0.s2plugin", "output package")
	flag.Parse()

	read := func(path string) []byte {
		data, err := os.ReadFile(filepath.Join(*root, filepath.FromSlash(path)))
		if err != nil {
			panic(err)
		}
		return data
	}
	var m manifest
	if err := json.Unmarshal(read("manifest.source.json"), &m); err != nil {
		panic(err)
	}
	runtimePath := "runtimes/linux-amd64/plugin"
	uiPath := "ui/index.html"
	files := map[string][]byte{runtimePath: read(*binary), uiPath: read(*ui)}
	m.Runtimes = map[string]runtimeSpec{"linux-amd64": {Path: runtimePath}}
	m.UI = map[string]string{"entrypoint": uiPath}
	m.Files = make(map[string]string, len(files))
	for path, data := range files {
		digest := sha256.Sum256(data)
		m.Files[path] = hex.EncodeToString(digest[:])
	}
	manifestRaw, err := json.Marshal(m)
	if err != nil {
		panic(err)
	}
	encodedPrivate := strings.TrimSpace(string(read(*privateKeyPath)))
	privateKey, err := base64.StdEncoding.DecodeString(encodedPrivate)
	if err != nil || len(privateKey) != ed25519.PrivateKeySize {
		panic("invalid Ed25519 private key")
	}
	signatureRaw, err := json.Marshal(signature{
		Algorithm: "ed25519", KeyID: *keyID,
		Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(ed25519.PrivateKey(privateKey), manifestRaw)),
	})
	if err != nil {
		panic(err)
	}
	if err := os.MkdirAll(filepath.Dir(*output), 0o755); err != nil {
		panic(err)
	}
	file, err := os.Create(*output)
	if err != nil {
		panic(err)
	}
	writer := zip.NewWriter(file)
	writeEntry(writer, "manifest.json", manifestRaw, 0o644)
	writeEntry(writer, "signature.json", signatureRaw, 0o644)
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		mode := os.FileMode(0o644)
		if path == runtimePath {
			mode = 0o755
		}
		writeEntry(writer, path, files[path], mode)
	}
	if err := writer.Close(); err != nil {
		panic(err)
	}
	if err := file.Close(); err != nil {
		panic(err)
	}
	publicKey := ed25519.PrivateKey(privateKey).Public().(ed25519.PublicKey)
	publicKeyBase64 := base64.StdEncoding.EncodeToString(publicKey)
	distDir := filepath.Dir(*output)
	if err := os.WriteFile(filepath.Join(distDir, "publisher-public-key.txt"), []byte(publicKeyBase64+"\n"), 0o644); err != nil {
		panic(err)
	}
	trustedPublisher := fmt.Sprintf("plugins:\n  allow_unsigned: false\n  trusted_publishers:\n    %s: %q\n", *keyID, publicKeyBase64)
	if err := os.WriteFile(filepath.Join(distDir, "trusted-publisher.yaml"), []byte(trustedPublisher), 0o644); err != nil {
		panic(err)
	}
	packageData, err := os.ReadFile(*output)
	if err != nil {
		panic(err)
	}
	packageDigest := sha256.Sum256(packageData)
	checksum := fmt.Sprintf("%s  %s\n", hex.EncodeToString(packageDigest[:]), filepath.Base(*output))
	if err := os.WriteFile(filepath.Join(distDir, "SHA256SUMS"), []byte(checksum), 0o644); err != nil {
		panic(err)
	}
	fmt.Println(*output)
}

func writeEntry(writer *zip.Writer, name string, data []byte, mode os.FileMode) {
	header := &zip.FileHeader{Name: name, Method: zip.Deflate}
	header.SetMode(mode)
	header.SetModTime(time.Date(1980, time.January, 1, 0, 0, 0, 0, time.UTC))
	entry, err := writer.CreateHeader(header)
	if err != nil {
		panic(err)
	}
	if _, err := io.Copy(entry, bytes.NewReader(data)); err != nil {
		panic(err)
	}
}
