package buildinfo

import (
	"encoding/json"
	"testing"
)

func TestJSONExposesCompleteBuildIdentity(t *testing.T) {
	var info Info
	if err := json.Unmarshal([]byte(JSON()), &info); err != nil {
		t.Fatal(err)
	}
	if info.Version == "" || info.Revision == "" || info.BuiltAt == "" || info.Go == "" || info.OS == "" || info.Arch == "" {
		t.Fatalf("incomplete build identity: %+v", info)
	}
}
