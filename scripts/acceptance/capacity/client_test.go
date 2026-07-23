package main

import (
	"errors"
	"net"
	"net/url"
	"testing"
)

func TestSafeDialFailureExcludesPostConnectFailures(t *testing.T) {
	dialFailure := &url.Error{Err: &net.OpError{Op: "dial", Err: errors.New("connection was not established")}}
	readFailure := &url.Error{Err: &net.OpError{Op: "read", Err: errors.New("response was interrupted")}}

	if !isSafeDialFailure(dialFailure) {
		t.Fatal("an unsent dial failure was not eligible for bounded recovery")
	}
	if isSafeDialFailure(readFailure) {
		t.Fatal("a post-connect read failure was eligible for replay")
	}
	if errorClass(dialFailure) != "net_dial" || errorClass(readFailure) != "net_read" {
		t.Fatal("network failure reporting did not preserve the sending boundary")
	}
}
