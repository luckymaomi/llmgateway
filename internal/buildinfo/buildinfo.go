package buildinfo

import (
	"encoding/json"
	"runtime"
)

var (
	version  = "development"
	revision = "unknown"
	builtAt  = "unknown"
)

type Info struct {
	Version  string `json:"version"`
	Revision string `json:"revision"`
	BuiltAt  string `json:"builtAt"`
	Go       string `json:"go"`
	OS       string `json:"os"`
	Arch     string `json:"arch"`
}

func Current() Info {
	return Info{Version: version, Revision: revision, BuiltAt: builtAt, Go: runtime.Version(), OS: runtime.GOOS, Arch: runtime.GOARCH}
}

func JSON() string {
	encoded, err := json.Marshal(Current())
	if err != nil {
		panic(err)
	}
	return string(encoded)
}
