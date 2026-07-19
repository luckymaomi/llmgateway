package canonical

import (
	"reflect"
	"testing"
)

func TestSSEDecoderReassemblesArbitraryChunks(t *testing.T) {
	t.Parallel()

	input := []byte("\xef\xbb\xbf: heartbeat\r\nid: request-7\r\nevent: completion\r\nretry: 1500\r\ndata: {\"part\":\r\ndata: \"ready\"}\r\n\r\ndata: [DONE]\n\n")
	decoder := NewSSEDecoder(4096)
	var events []SSEEvent
	chunkPattern := []int{1, 2, 5, 3, 8}
	for offset, patternIndex := 0, 0; offset < len(input); patternIndex++ {
		chunkBytes := chunkPattern[patternIndex%len(chunkPattern)]
		end := offset + chunkBytes
		if end > len(input) {
			end = len(input)
		}
		decoded, err := decoder.Feed(input[offset:end])
		if err != nil {
			t.Fatalf("feed SSE chunk: %v", err)
		}
		events = append(events, decoded...)
		offset = end
	}
	decoded, err := decoder.Close()
	if err != nil {
		t.Fatalf("close SSE decoder: %v", err)
	}
	events = append(events, decoded...)

	if len(events) != 2 {
		t.Fatalf("event count = %d, want 2", len(events))
	}
	retryMilliseconds := int64(1500)
	wantFirst := SSEEvent{
		Event: "completion", Data: []byte("{\"part\":\n\"ready\"}"), ID: "request-7",
		RetryMilliseconds: &retryMilliseconds,
	}
	if !reflect.DeepEqual(events[0], wantFirst) {
		t.Fatalf("first event = %#v, want %#v", events[0], wantFirst)
	}
	if string(events[1].Data) != "[DONE]" || events[1].ID != "request-7" {
		t.Fatalf("completion event = %#v", events[1])
	}
}

func TestSSEDecoderDispatchesFinalEventAtEOF(t *testing.T) {
	t.Parallel()

	decoder := NewSSEDecoder(1024)
	if _, err := decoder.Feed([]byte("data: {\"status\":\"complete\"}")); err != nil {
		t.Fatalf("feed SSE event: %v", err)
	}
	events, err := decoder.Close()
	if err != nil {
		t.Fatalf("close SSE decoder: %v", err)
	}
	if len(events) != 1 || string(events[0].Data) != `{"status":"complete"}` {
		t.Fatalf("events = %#v", events)
	}
}
