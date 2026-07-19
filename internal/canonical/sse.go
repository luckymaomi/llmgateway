package canonical

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
)

const DefaultMaxSSEEventBytes = 1 << 20

type SSEEvent struct {
	Event             string
	Data              []byte
	ID                string
	RetryMilliseconds *int64
}

type SSEDecoder struct {
	maxEventBytes int
	buffer        []byte
	data          []byte
	event         string
	id            string
	retry         *int64
	seenData      bool
	firstLine     bool
	closed        bool
}

func NewSSEDecoder(maxEventBytes int) *SSEDecoder {
	if maxEventBytes <= 0 {
		maxEventBytes = DefaultMaxSSEEventBytes
	}
	return &SSEDecoder{maxEventBytes: maxEventBytes, firstLine: true}
}

func (d *SSEDecoder) Feed(chunk []byte) ([]SSEEvent, error) {
	if d.closed {
		return nil, errors.New("SSE decoder is closed")
	}
	if len(chunk) == 0 {
		return nil, nil
	}
	d.buffer = append(d.buffer, chunk...)
	events, err := d.consumeLines(false)
	if err != nil {
		return nil, err
	}
	if len(d.buffer) > d.maxEventBytes {
		return nil, fmt.Errorf("SSE line exceeds %d bytes", d.maxEventBytes)
	}
	return events, nil
}

func (d *SSEDecoder) Close() ([]SSEEvent, error) {
	if d.closed {
		return nil, errors.New("SSE decoder is closed")
	}
	d.closed = true
	events, err := d.consumeLines(true)
	if err != nil {
		return nil, err
	}
	if event, ok := d.dispatch(); ok {
		events = append(events, event)
	}
	return events, nil
}

func (d *SSEDecoder) consumeLines(atEOF bool) ([]SSEEvent, error) {
	var events []SSEEvent
	for {
		line, consumed, ok := nextSSELine(d.buffer, atEOF)
		if !ok {
			break
		}
		d.buffer = d.buffer[consumed:]
		if len(line) > d.maxEventBytes {
			return nil, fmt.Errorf("SSE line exceeds %d bytes", d.maxEventBytes)
		}
		if d.firstLine {
			d.firstLine = false
			line = bytes.TrimPrefix(line, []byte{0xef, 0xbb, 0xbf})
		}
		if len(line) == 0 {
			if event, dispatch := d.dispatch(); dispatch {
				events = append(events, event)
			}
			continue
		}
		if err := d.consumeField(line); err != nil {
			return nil, err
		}
	}
	return events, nil
}

func nextSSELine(buffer []byte, atEOF bool) ([]byte, int, bool) {
	for index, character := range buffer {
		switch character {
		case '\n':
			end := index
			if end > 0 && buffer[end-1] == '\r' {
				end--
			}
			return buffer[:end], index + 1, true
		case '\r':
			if index+1 == len(buffer) && !atEOF {
				return nil, 0, false
			}
			consumed := index + 1
			if consumed < len(buffer) && buffer[consumed] == '\n' {
				consumed++
			}
			return buffer[:index], consumed, true
		}
	}
	if atEOF && len(buffer) > 0 {
		return buffer, len(buffer), true
	}
	return nil, 0, false
}

func (d *SSEDecoder) consumeField(line []byte) error {
	if line[0] == ':' {
		return nil
	}
	field, value, found := bytes.Cut(line, []byte{':'})
	if !found {
		value = nil
	} else if len(value) > 0 && value[0] == ' ' {
		value = value[1:]
	}

	switch string(field) {
	case "data":
		additionalBytes := len(value)
		if d.seenData {
			additionalBytes++
		}
		if len(d.data)+additionalBytes > d.maxEventBytes {
			return fmt.Errorf("SSE event exceeds %d bytes", d.maxEventBytes)
		}
		if d.seenData {
			d.data = append(d.data, '\n')
		}
		d.data = append(d.data, value...)
		d.seenData = true
	case "event":
		d.event = string(value)
	case "id":
		if !bytes.ContainsRune(value, 0) {
			d.id = string(value)
		}
	case "retry":
		milliseconds, err := strconv.ParseInt(string(value), 10, 64)
		if err == nil && milliseconds >= 0 {
			d.retry = &milliseconds
		}
	}
	return nil
}

func (d *SSEDecoder) dispatch() (SSEEvent, bool) {
	if !d.seenData {
		d.event = ""
		d.retry = nil
		return SSEEvent{}, false
	}
	event := SSEEvent{
		Event:             d.event,
		Data:              append([]byte(nil), d.data...),
		ID:                d.id,
		RetryMilliseconds: d.retry,
	}
	d.data = d.data[:0]
	d.event = ""
	d.retry = nil
	d.seenData = false
	return event, true
}
