package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

func main() {
	directory := flag.String("dir", "build/keys", "key output directory")
	flag.Parse()
	if err := os.MkdirAll(*directory, 0o700); err != nil {
		panic(err)
	}
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		panic(err)
	}
	privatePath := filepath.Join(*directory, "publisher.private")
	publicPath := filepath.Join(*directory, "publisher.public")
	if err := os.WriteFile(privatePath, []byte(base64.StdEncoding.EncodeToString(privateKey)+"\n"), 0o600); err != nil {
		panic(err)
	}
	if err := os.WriteFile(publicPath, []byte(base64.StdEncoding.EncodeToString(publicKey)+"\n"), 0o644); err != nil {
		panic(err)
	}
	fmt.Println(publicPath)
}
