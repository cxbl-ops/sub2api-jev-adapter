package main

import (
	"github.com/cxbl-ops/sub2api-jev-adapter/internal/adapter"
	pluginv1 "github.com/cxbl-ops/sub2api-jev-adapter/sdk/pluginapi/v1"
)

var version = "0.1.0"

func main() {
	pluginv1.Serve(adapter.New(version))
}
