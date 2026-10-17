// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package lifecycleprocess

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/lifecycle"
	"linkd/internal/store/storetest"
)

func TestStrategyHookBurstDoesNotSerializeBehindTwoConnections(t *testing.T) {
	// 挡住已进入 Redis 的 EVAL，确认同一来源的独立事件能并发提交提示，
	// 而不是在两个连接后排队到超时。服务在测试内启动，不依赖外部 Redis。
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{}, 8)
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	var servers sync.WaitGroup
	servers.Go(func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			servers.Go(func() {
				defer func() { _ = conn.Close() }()
				reader := bufio.NewReader(conn)
				for {
					command, err := readHookRedisCommand(reader)
					if err != nil {
						return
					}
					reply := "+OK\r\n"
					switch strings.ToLower(command) {
					case "hello":
						reply = "%0\r\n"
					case "eval":
						entered <- struct{}{}
						<-release
						reply = ":1\r\n"
					}
					if _, err := io.WriteString(conn, reply); err != nil {
						return
					}
				}
			})
		}
	})
	ms := int64(3000)
	hooks, closeHooks, err := openHooks([]config.HookConfig{{
		Name: "active", Type: config.HookTypeActiveAlertByStrategy,
		Config: config.HookParameters{Redis: &config.RedisConfig{Address: listener.Addr().String()}, KeyPrefix: "active", TimeoutMilliseconds: &ms},
	}}, nil)
	if err != nil {
		_ = listener.Close()
		servers.Wait()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		unblock()
		_ = closeHooks()
		_ = listener.Close()
		servers.Wait()
	})
	results := make(chan error, 8)
	for range 8 {
		go func() {
			alert := storetest.Alert("tenant", "alert", "event", "fp", "warning")
			alert.Labels = domain.DimensionMap{"strategy_id": domain.NewStringScalar("123")}
			_, err := hooks[0].Execute(context.Background(), lifecycle.FinalHookInput{
				Alert: alert, Cause: lifecycle.AlertChangeCause{Type: lifecycle.AlertChangeCauseSourceEvent, ID: "event"},
			})
			results <- err
		}()
	}
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	for i := range 8 {
		select {
		case <-entered:
		case <-timer.C:
			t.Fatalf("only %d independent events reached Redis; others are waiting for the hook pool", i)
		}
	}
	unblock()
	for range 8 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
}

func readHookRedisCommand(reader *bufio.Reader) (string, error) {
	header, err := reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	count, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(header, "*")))
	if err != nil {
		return "", err
	}
	var command string
	for i := range count {
		header, err = reader.ReadString('\n')
		if err != nil {
			return "", err
		}
		length, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(header, "$")))
		if err != nil || length < 0 {
			return "", fmt.Errorf("invalid test Redis command length")
		}
		data := make([]byte, length+2)
		if _, err := io.ReadFull(reader, data); err != nil {
			return "", err
		}
		if i == 0 {
			command = string(data[:length])
		}
	}
	return command, nil
}
