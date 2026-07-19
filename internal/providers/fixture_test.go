package providers

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"testing"
)

func assertRequestJSON(t *testing.T, request *http.Request, want string) {
	t.Helper()
	body, err := io.ReadAll(request.Body)
	if err != nil {
		t.Fatalf("read request body: %v", err)
	}
	assertJSON(t, body, []byte(want))
}

func assertJSON(t *testing.T, got, want []byte) {
	t.Helper()
	decode := func(value []byte) any {
		t.Helper()
		decoder := json.NewDecoder(bytes.NewReader(value))
		decoder.UseNumber()
		var decoded any
		if err := decoder.Decode(&decoded); err != nil {
			t.Fatalf("decode JSON fixture: %v\n%s", err, value)
		}
		return decoded
	}
	gotValue := decode(got)
	wantValue := decode(want)
	if !reflect.DeepEqual(gotValue, wantValue) {
		gotFormatted, _ := json.MarshalIndent(gotValue, "", "  ")
		wantFormatted, _ := json.MarshalIndent(wantValue, "", "  ")
		t.Fatalf("JSON mismatch\ngot:\n%s\nwant:\n%s", gotFormatted, wantFormatted)
	}
}

func boolPointer(value bool) *bool {
	return &value
}
