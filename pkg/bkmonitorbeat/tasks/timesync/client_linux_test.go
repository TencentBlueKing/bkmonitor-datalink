//go:build linux

package timesync

import (
	"net"
	"strings"
	"testing"
	"time"
)

func TestQueryChronyTimeoutClosesConn(t *testing.T) {
	originalNewChronyConn := newChronyConn
	defer func() {
		newChronyConn = originalNewChronyConn
	}()

	clientConn, serverConn := net.Pipe()
	newChronyConn = func(addr string, timeout time.Duration) (net.Conn, error) {
		return clientConn, nil
	}

	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		defer serverConn.Close()

		buf := make([]byte, 1024)
		_, _ = serverConn.Read(buf)
		time.Sleep(200 * time.Millisecond)
	}()

	cli := NewClient(&Option{
		ChronyAddr: "[::1]:323",
		Timeout:    50 * time.Millisecond,
	})

	start := time.Now()
	_, err := cli.queryChrony()
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "timeout") {
		t.Fatalf("expected timeout-related error, got %v", err)
	}
	if elapsed > 150*time.Millisecond {
		t.Fatalf("expected queryChrony to return near timeout, took %v", elapsed)
	}

	select {
	case <-serverDone:
	case <-time.After(time.Second):
		t.Fatal("server side connection was not closed in time")
	}
}
