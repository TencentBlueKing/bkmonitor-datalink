// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package metric

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/redistest"
)

// serverBytes reads what a server says it has taken in and sent out, over the
// test's own warm connection. Its own request and reply are worked out and
// handed back, because the counters include them: the request of the INFO that
// closes a window is read before its reply is built, and the reply of the INFO
// that opens one is written after it was built.
func serverBytes(t *testing.T, admin *redis.Client) (in, out int64, replySize int64) {
	t.Helper()
	reply, err := admin.Info(context.Background(), "stats").Result()
	if err != nil {
		t.Fatal(err)
	}
	read := func(name string) int64 {
		for _, line := range strings.Split(reply, "\r\n") {
			if value, found := strings.CutPrefix(line, name+":"); found {
				parsed, err := strconv.ParseInt(value, 10, 64)
				if err != nil {
					t.Fatal(err)
				}
				return parsed
			}
		}
		t.Fatalf("INFO stats has no %s", name)
		return 0
	}
	return read("total_net_input_bytes"), read("total_net_output_bytes"), int64(bulkBytes(len(reply)))
}

// The expectation comes from outside the code under test: the server's own
// count of the bytes it read and wrote, one reply type at a time, so that one
// rarely seen type miscounted cannot hide in a total.
func TestBytesMatchWhatTheServerCountedForEachReplyType(t *testing.T) {
	server := redistest.Start(t)
	if server == nil {
		return
	}
	ctx := context.Background()
	admin := server.Client()
	seed := func() {
		for _, err := range []error{
			admin.FlushDB(ctx).Err(),
			admin.Set(ctx, "string", "a value of some length", 0).Err(),
			admin.Set(ctx, "expiring", "x", time.Hour).Err(),
			admin.Set(ctx, "counter", "41", 0).Err(),
			// More than five fields, so that a header counting pairs rather than
			// elements has a different number of digits.
			admin.HSet(ctx, "hash", "field-1", "value-1", "field-2", "a longer value", "field-3", "3", "field-4", "4", "field-5", "5", "field-6", "6").Err(),
			admin.RPush(ctx, "list", "one", "two", "three").Err(),
			admin.SAdd(ctx, "set", "member-1", "member-2").Err(),
			admin.ZAdd(ctx, "zset", &redis.Z{Score: 1, Member: "a"}, &redis.Z{Score: 1700000000000, Member: "bb"}, &redis.Z{Score: 1.5, Member: "ccc"}).Err(),
		} {
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	hash := "return {1, 'text', {2, 'nested', {3}}, false}"
	for _, test := range []struct {
		name string
		run  func(client *redis.Client) error
		// tolerance is the bytes a reply worked back from its parsed form may
		// be off by, and only where the parsed form has lost the difference.
		tolerance int64
	}{
		{name: "bulk string", run: func(c *redis.Client) error { return c.Get(ctx, "string").Err() }},
		{name: "nil bulk string", run: func(c *redis.Client) error { return ignoreNil(c.Get(ctx, "absent").Err()) }},
		{name: "status", run: func(c *redis.Client) error { return c.Set(ctx, "written", "value", 0).Err() }},
		{name: "ping", run: func(c *redis.Client) error { return c.Ping(ctx).Err() }},
		{name: "integer", run: func(c *redis.Client) error { return c.IncrBy(ctx, "counter", 1000).Err() }},
		{name: "negative integer", run: func(c *redis.Client) error { return c.IncrBy(ctx, "counter", -5000).Err() }},
		{name: "strlen", run: func(c *redis.Client) error { return c.StrLen(ctx, "string").Err() }},
		{name: "del", run: func(c *redis.Client) error { return c.Del(ctx, "string", "absent").Err() }},
		{name: "boolean expire", run: func(c *redis.Client) error { return c.Expire(ctx, "string", time.Hour).Err() }},
		{name: "boolean hexists", run: func(c *redis.Client) error { return c.HExists(ctx, "hash", "field-1").Err() }},
		{name: "setnx", run: func(c *redis.Client) error { return c.SetNX(ctx, "fresh", "v", 0).Err() }},
		{name: "set nx with a life, taken", run: func(c *redis.Client) error { return c.SetNX(ctx, "fresh-2", "v", time.Hour).Err() }},
		{name: "set nx with a life, refused", run: func(c *redis.Client) error { return c.SetNX(ctx, "string", "v", time.Hour).Err() }},
		{name: "ttl", run: func(c *redis.Client) error { return c.TTL(ctx, "expiring").Err() }},
		{name: "pttl", run: func(c *redis.Client) error { return c.PTTL(ctx, "expiring").Err() }, tolerance: 0},
		{name: "ttl with no life", run: func(c *redis.Client) error { return c.TTL(ctx, "string").Err() }},
		{name: "pttl of nothing", run: func(c *redis.Client) error { return c.PTTL(ctx, "absent").Err() }},
		{name: "mget with a gap", run: func(c *redis.Client) error { return c.MGet(ctx, "string", "absent", "counter").Err() }},
		{name: "hmget", run: func(c *redis.Client) error { return c.HMGet(ctx, "hash", "field-1", "absent").Err() }},
		{name: "hgetall", run: func(c *redis.Client) error { return c.HGetAll(ctx, "hash").Err() }},
		{name: "hgetall of nothing", run: func(c *redis.Client) error { return c.HGetAll(ctx, "absent").Err() }},
		{name: "hkeys", run: func(c *redis.Client) error { return c.HKeys(ctx, "hash").Err() }},
		{name: "lrange", run: func(c *redis.Client) error { return c.LRange(ctx, "list", 0, -1).Err() }},
		{name: "smembers", run: func(c *redis.Client) error { return c.SMembers(ctx, "set").Err() }},
		{name: "zrange", run: func(c *redis.Client) error { return c.ZRange(ctx, "zset", 0, -1).Err() }},
		{name: "zcard", run: func(c *redis.Client) error { return c.ZCard(ctx, "zset").Err() }},
		{name: "scan", run: func(c *redis.Client) error { return c.Scan(ctx, 0, "*", 100).Err() }},
		{name: "hscan", run: func(c *redis.Client) error { return c.HScan(ctx, "hash", 0, "*", 100).Err() }},
		{name: "dbsize", run: func(c *redis.Client) error { return c.DBSize(ctx).Err() }},
		{name: "eval to an integer", run: func(c *redis.Client) error { return c.Eval(ctx, "return 12345", nil).Err() }},
		{name: "eval to a string", run: func(c *redis.Client) error { return c.Eval(ctx, "return 'text'", nil).Err() }},
		{name: "eval to nothing", run: func(c *redis.Client) error { return ignoreNil(c.Eval(ctx, "return false", nil).Err()) }},
		{name: "eval to an array holding an error", run: func(c *redis.Client) error {
			return c.Eval(ctx, "return {1, redis.error_reply('nested failure')}", nil).Err()
		}},
		{name: "eval to nested arrays", run: func(c *redis.Client) error { return c.Eval(ctx, hash, []string{"k1"}, "a1", 7).Err() }},
		// A status nested in a script's reply parses to the same string a bulk
		// would: the one difference the parsed form has lost.
		{name: "eval to a status", run: func(c *redis.Client) error { return c.Eval(ctx, "return redis.status_reply('OK')", nil).Err() }, tolerance: 3},
		{name: "evalsha", run: func(c *redis.Client) error { return c.EvalSha(ctx, redis.NewScript(hash).Hash(), []string{"k1"}).Err() }},
		{name: "script load", run: func(c *redis.Client) error { return c.ScriptLoad(ctx, hash).Err() }},
		{name: "an error reply from a script", run: func(c *redis.Client) error {
			return ignoreReply(c.Eval(ctx, "return redis.error_reply('BOOM it failed')", nil).Err())
		}},
		{name: "a wrong type", run: func(c *redis.Client) error { return ignoreReply(c.Get(ctx, "hash").Err()) }},
		{name: "a cold script cache", run: func(c *redis.Client) error { return ignoreReply(c.EvalSha(ctx, strings.Repeat("0", 40), nil).Err()) }},
		{name: "a pipeline of every kind", run: func(c *redis.Client) error {
			_, err := c.Pipelined(ctx, func(p redis.Pipeliner) error {
				p.Get(ctx, "string")
				p.Get(ctx, "absent")
				p.Incr(ctx, "counter")
				p.HGetAll(ctx, "hash")
				p.Eval(ctx, "return redis.error_reply('in a batch')", nil)
				p.Eval(ctx, hash, []string{"k1"})
				return nil
			})
			return ignoreReply(ignoreNil(err))
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			seed()
			if err := admin.ScriptLoad(ctx, hash).Err(); err != nil {
				t.Fatal(err)
			}
			recorder := NewRecorder(BuildInfo{})
			client := redis.NewClient(&redis.Options{Addr: server.Addr, MaxRetries: -1, PoolSize: 1})
			defer client.Close()
			// The connection's setup is not counted: open it before the window.
			if err := client.Ping(ctx).Err(); err != nil {
				t.Fatal(err)
			}
			client.AddHook(recorder.RedisHook("runtime"))

			beforeIn, beforeOut, openingReply := serverBytes(t, admin)
			if err := test.run(client); err != nil {
				t.Fatalf("the command failed: %v", err)
			}
			afterIn, afterOut, _ := serverBytes(t, admin)

			closingRequest := int64(sentBytes([]interface{}{"info", "stats"}))
			wantSent := afterIn - beforeIn - closingRequest
			wantReceived := afterOut - beforeOut - openingReply
			gotSent := int64(redisCounterSum(t, recorder, "alarmd_redis_bytes_total", map[string]string{"direction": "sent"}))
			gotReceived := int64(redisCounterSum(t, recorder, "alarmd_redis_bytes_total", map[string]string{"direction": "received"}))
			if gotSent != wantSent {
				t.Errorf("sent %d bytes, the server read %d", gotSent, wantSent)
			}
			if off := gotReceived - wantReceived; off > test.tolerance || off < -test.tolerance {
				t.Errorf("received %d bytes, the server wrote %d", gotReceived, wantReceived)
			}
		})
	}
}

func ignoreNil(err error) error {
	if err == redis.Nil {
		return nil
	}
	return err
}

func ignoreReply(err error) error {
	if _, replied := err.(redis.Error); replied {
		return nil
	}
	return err
}

// Over a mixed load, the total the design holds the family to.
func TestBytesOverAMixedLoadAreWithinTwoPercentOfTheServer(t *testing.T) {
	server := redistest.Start(t)
	if server == nil {
		return
	}
	ctx := context.Background()
	admin := server.Client()
	recorder := NewRecorder(BuildInfo{})
	client := redis.NewClient(&redis.Options{Addr: server.Addr, MaxRetries: -1, PoolSize: 1})
	defer client.Close()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	client.AddHook(recorder.RedisHook("runtime"))
	script := redis.NewScript("redis.call('PSETEX', KEYS[1], ARGV[1], ARGV[2]); return {1, redis.call('GET', KEYS[1])}")

	beforeIn, beforeOut, openingReply := serverBytes(t, admin)
	value := strings.Repeat("v", 1287)
	for round := 0; round < 200; round++ {
		key := fmt.Sprintf("key-%d", round)
		if err := script.Run(ctx, client, []string{key}, 60000, value).Err(); err != nil {
			t.Fatal(err)
		}
		if err := client.MGet(ctx, key, "absent").Err(); err != nil {
			t.Fatal(err)
		}
		if _, err := client.Pipelined(ctx, func(p redis.Pipeliner) error {
			p.HSet(ctx, "hash", key, round)
			p.HGet(ctx, "hash", key)
			p.PExpire(ctx, key, time.Hour)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	afterIn, afterOut, _ := serverBytes(t, admin)

	wantSent := float64(afterIn - beforeIn - int64(sentBytes([]interface{}{"info", "stats"})))
	wantReceived := float64(afterOut - beforeOut - openingReply)
	gotSent := redisCounterSum(t, recorder, "alarmd_redis_bytes_total", map[string]string{"direction": "sent"})
	gotReceived := redisCounterSum(t, recorder, "alarmd_redis_bytes_total", map[string]string{"direction": "received"})
	if off := math.Abs(gotSent-wantSent) / wantSent; off > 0.02 {
		t.Errorf("sent %v bytes, the server read %v: %.2f%% off", gotSent, wantSent, off*100)
	}
	if off := math.Abs(gotReceived-wantReceived) / wantReceived; off > 0.02 {
		t.Errorf("received %v bytes, the server wrote %v: %.2f%% off", gotReceived, wantReceived, off*100)
	}
}
