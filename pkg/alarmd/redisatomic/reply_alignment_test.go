// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package redisatomic

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
)

// What these tests guard against: a transaction whose MULTI is refused with
// a server reply leaves the replies of the queued commands and of EXEC on
// the connection. go-redis v8.11.5 (redis.go:501-503, in txPipelineReadQueued;
// v9 the same) returns on the MULTI reply without reading them, and hands
// the connection back to the pool as healthy; its only guard is the bytes
// already in its read buffer. When the remaining replies arrive a moment
// later - the server read the pipeline in two pieces, which a pipeline over
// the 4 KiB write buffer makes possible - the next command on that
// connection reads one of them as its own reply, with no error.
//
// The fake server stands in for one leaving the LOADING state while it
// reads a call: the call's first command is refused, and whatever follows
// it in the same call is answered only after a delay, as if run once
// loading ended. GET k is answered "fresh" at once: a GET that reads
// anything else read a reply that was not its own.

// readCommand reads one RESP array command: its upper-cased name and its
// arguments.
func readCommand(rd *bufio.Reader) (string, []string, error) {
	line, err := rd.ReadString('\n')
	if err != nil {
		return "", nil, err
	}
	n, err := strconv.Atoi(strings.TrimSpace(line[1:]))
	if err != nil {
		return "", nil, err
	}
	args := make([]string, 0, n)
	for i := 0; i < n; i++ {
		header, err := rd.ReadString('\n')
		if err != nil {
			return "", nil, err
		}
		size, err := strconv.Atoi(strings.TrimSpace(header[1:]))
		if err != nil {
			return "", nil, err
		}
		buf := make([]byte, size+2)
		if _, err := io.ReadFull(rd, buf); err != nil {
			return "", nil, err
		}
		args = append(args, string(buf[:size]))
	}
	return strings.ToUpper(args[0]), args[1:], nil
}

// loadingServer serves one connection the way the note above says, and
// returns its address and a channel that has its error, if any, once the
// client is gone.
func loadingServer(t *testing.T) (string, <-chan error) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	served := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			served <- err
			return
		}
		defer conn.Close()
		rd := bufio.NewReader(conn)
		write := func(s string) { _, _ = conn.Write([]byte(s)) }
		const loading = "-LOADING Redis is loading the dataset in memory\r\n"
		for {
			name, args, err := readCommand(rd)
			if err != nil {
				served <- nil
				return
			}
			switch {
			case name == "MULTI", name == "EVALSHA", name == "EVAL":
				write(loading)
			case name == "GET" && args[0] == "refused":
				write(loading)
			case name == "SET", name == "GET" && args[0] == "late":
				// Loading ends meanwhile: this runs on its own.
				time.Sleep(100 * time.Millisecond)
				if name == "SET" {
					write("+OK\r\n")
				} else {
					write("$4\r\nlate\r\n")
				}
			case name == "EXEC":
				write("-ERR EXEC without MULTI\r\n")
			case name == "GET" && args[0] == "k":
				write("$5\r\nfresh\r\n")
			default:
				served <- fmt.Errorf("unexpected command %s %v", name, args)
				return
			}
		}
	}()
	return listener.Addr().String(), served
}

// nextGet is what a GET on the same connection reads once the refused
// call's replies, if it left any, have reached the socket.
func nextGet(t *testing.T, client *redis.Client) (string, error) {
	t.Helper()
	time.Sleep(300 * time.Millisecond)
	return client.Get(context.Background(), "k").Result()
}

func oneConnection(t *testing.T, address string) *redis.Client {
	t.Helper()
	client := redis.NewClient(&redis.Options{Addr: address, PoolSize: 1, MaxRetries: -1, ReadTimeout: 2 * time.Second})
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// The counterexample, run first so the other two prove something: on this
// server a refused MULTI does leave a reply for the next command. If a
// go-redis upgrade makes this fail, the client reads the rest of a refused
// transaction now, and the reason for this package should be re-read.
func TestARefusedMultiLeavesItsRepliesForTheNextCommand(t *testing.T) {
	address, served := loadingServer(t)
	client := oneConnection(t, address)
	ctx := context.Background()
	_, err := client.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
		pipe.Set(ctx, "k", "v", 0)
		return nil
	})
	if err == nil || !strings.HasPrefix(err.Error(), "LOADING") {
		t.Fatalf("transaction err = %v, want the LOADING refusal", err)
	}
	if got, err := nextGet(t, client); err == nil && got == "fresh" {
		t.Fatal("the GET after a refused MULTI read its own reply: the stale-reply hazard is gone from this go-redis")
	}
	_ = client.Close()
	if err := <-served; err != nil {
		t.Fatal(err)
	}
}

// A write through this package that the server refuses leaves nothing on
// the connection: it is one command with one reply, so the next command
// reads its own.
func TestARefusedScriptWriteLeavesNothingForTheNextCommand(t *testing.T) {
	address, served := loadingServer(t)
	client := oneConnection(t, address)
	write := New(`redis.call('SET', KEYS[1], ARGV[1]) redis.call('SET', KEYS[2], ARGV[1]) return 1`)
	err := write.Run(context.Background(), client, []string{"k", "k2"}, "v")
	if err == nil || !strings.HasPrefix(err.Error(), "LOADING") {
		t.Fatalf("script err = %v, want the LOADING refusal and no EVAL after it", err)
	}
	if got, err := nextGet(t, client); err != nil || got != "fresh" {
		t.Fatalf("GET after the refused script = %q, %v; want \"fresh\": it read a reply of the script", got, err)
	}
	_ = client.Close()
	if err := <-served; err != nil {
		t.Fatal(err)
	}
}

// A plain pipeline - the reads of stores alarmd does not own - whose first
// command is refused still reads every reply, the late one included
// (redis.go:452-461), so the next command reads its own.
func TestARefusedCommandInAPipelineLeavesNothingForTheNextCommand(t *testing.T) {
	address, served := loadingServer(t)
	client := oneConnection(t, address)
	ctx := context.Background()
	var refused, late *redis.StringCmd
	_, _ = client.Pipelined(ctx, func(pipe redis.Pipeliner) error {
		refused = pipe.Get(ctx, "refused")
		late = pipe.Get(ctx, "late")
		return nil
	})
	if err := refused.Err(); err == nil || !strings.HasPrefix(err.Error(), "LOADING") {
		t.Fatalf("first command err = %v, want the LOADING refusal", err)
	}
	if got, err := late.Result(); err != nil || got != "late" {
		t.Fatalf("second command = %q, %v; want its own late reply", got, err)
	}
	if got, err := nextGet(t, client); err != nil || got != "fresh" {
		t.Fatalf("GET after the pipeline = %q, %v; want \"fresh\": it read a reply of the pipeline", got, err)
	}
	_ = client.Close()
	if err := <-served; err != nil {
		t.Fatal(err)
	}
}
