// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package redisbatch

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/go-redis/redis/v8"
)

// fakeClient serves pipelines of STRLENs and GETs from a map and records
// each pipeline's commands. A key in answered is answered with that error;
// fail fails the transport of every pipeline.
type fakeClient struct {
	values    map[string]string
	answered  map[string]error
	fail      error
	pipelines [][]string
}

type fakePipeline struct {
	redis.Pipeliner
	client   *fakeClient
	commands []string
}

// answeredError is Redis answering a command with an error.
type answeredError string

func (err answeredError) Error() string { return string(err) }
func (answeredError) RedisError()       {}

func (pipe *fakePipeline) StrLen(_ context.Context, key string) *redis.IntCmd {
	pipe.commands = append(pipe.commands, "STRLEN "+key)
	if err := pipe.client.answered[key]; err != nil {
		return redis.NewIntResult(0, err)
	}
	return redis.NewIntResult(int64(len(pipe.client.values[key])), pipe.client.fail)
}

func (pipe *fakePipeline) Get(_ context.Context, key string) *redis.StringCmd {
	pipe.commands = append(pipe.commands, "GET "+key)
	if err := pipe.client.answered[key]; err != nil {
		return redis.NewStringResult("", err)
	}
	value, found := pipe.client.values[key]
	if !found {
		return redis.NewStringResult("", redis.Nil)
	}
	return redis.NewStringResult(value, pipe.client.fail)
}

func (client *fakeClient) Pipelined(_ context.Context, fn func(redis.Pipeliner) error) ([]redis.Cmder, error) {
	pipe := &fakePipeline{client: client}
	if err := fn(pipe); err != nil {
		return nil, err
	}
	client.pipelines = append(client.pipelines, pipe.commands)
	return nil, client.fail
}

// keysOf is count keys, each holding a value size bytes long.
func keysOf(client *fakeClient, count, size int) []string {
	keys := make([]string, count)
	for index := range keys {
		keys[index] = fmt.Sprintf("k:%04d", index)
		client.values[keys[index]] = strings.Repeat("v", size)
	}
	return keys
}

// shape is each pipeline's command and count, in order: "STRLEN 20 GET 3".
func (client *fakeClient) shape() string {
	parts := make([]string, 0, len(client.pipelines))
	for _, pipeline := range client.pipelines {
		parts = append(parts, fmt.Sprintf("%s %d", strings.Fields(pipeline[0])[0], len(pipeline)))
	}
	return strings.Join(parts, " ")
}

// The first value is admitted whatever its length; after it, values while
// their lengths add up to at most the bound. A bound of zero or below admits
// the first alone, rather than everything a negative bound read unsigned
// would.
func TestFirstThenWithinAdmitsTheFirstAndThenWithinTheBound(t *testing.T) {
	for _, test := range []struct {
		bound int
		sizes []uint64
		want  string
	}{
		{bound: 10, sizes: []uint64{15, 1}, want: "[true false]"},
		{bound: 10, sizes: []uint64{4, 4, 2, 1}, want: "[true true true false]"},
		{bound: 0, sizes: []uint64{1, 1}, want: "[true false]"},
		{bound: -1, sizes: []uint64{1, 1}, want: "[true false]"},
	} {
		admit, got := FirstThenWithin(test.bound), []bool{}
		for _, size := range test.sizes {
			got = append(got, admit(size))
		}
		if fmt.Sprint(got) != test.want {
			t.Fatalf("bound %d over %v admitted %v, want %s", test.bound, test.sizes, got, test.want)
		}
	}
}

// Windows reads each key's length once, in a pipeline of up to 512 ahead of
// the windows, and each window's values in one pipeline: the values after
// the last whose lengths add up to at most the bound. Twenty values of ten
// bytes under a bound of 35 are six windows of three and one of two, after
// one pipeline of twenty lengths - not the lengths of every key left again
// for each window. A window never spans two pipelines of lengths: 1,030
// values under a bound none reaches are windows of 512, 512 and 6.
func TestWindowsReadEachLengthOnceAndEachWindowWithinTheBound(t *testing.T) {
	for _, test := range []struct {
		count, bound int
		want         string
	}{
		{count: 20, bound: 35, want: "STRLEN 20 GET 3 GET 3 GET 3 GET 3 GET 3 GET 3 GET 2"},
		{count: 1030, bound: 1 << 20, want: "STRLEN 512 GET 512 STRLEN 512 GET 512 STRLEN 6 GET 6"},
	} {
		client := &fakeClient{values: map[string]string{}}
		keys := keysOf(client, test.count, 10)
		windows, read := NewWindows(client, keys, test.bound), 0
		for {
			start, values, err := windows.Next(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if len(values) == 0 {
				break
			}
			if start != read {
				t.Fatalf("%d keys: a window at %d, want %d", test.count, start, read)
			}
			for _, value := range values {
				if string(value.Raw) != client.values[keys[read]] || value.Missing || value.Err != nil {
					t.Fatalf("%d keys: %s read %+v", test.count, keys[read], value)
				}
				read++
			}
		}
		if read != test.count || client.shape() != test.want {
			t.Fatalf("%d keys: read %d as %q, want all as %q", test.count, read, client.shape(), test.want)
		}
	}
}

// A missing key is a fact about it. A key Redis answers with an error is
// not: it carries the error in its place, the keys beside it read, and the
// window comes back with an UnansweredError counting such keys and naming
// the first, so a caller that stops on any error changes nothing for them.
// Every key answered with an error, as while Redis loads, is the same. A
// transport failure fails the read and leaves the window unread.
func TestWindowsAnswerEachKeyAndFailOnTheTransport(t *testing.T) {
	client := &fakeClient{values: map[string]string{"a": "1", "c": "3"},
		answered: map[string]error{"b": answeredError("WRONGTYPE Operation against a key holding the wrong kind of value")}}
	windows := NewWindows(client, []string{"a", "b", "c", "d"}, 1<<20)
	start, values, err := windows.Next(context.Background())
	var unanswered *UnansweredError
	if !errors.As(err, &unanswered) || unanswered.Keys != 1 || !strings.Contains(err.Error(), "b: WRONGTYPE") || start != 0 || len(values) != 4 {
		t.Fatalf("read %d values at %d, %v; want four and one key answered with an error", len(values), start, err)
	}
	if string(values[0].Raw) != "1" || !Answered(values[1].Err) || string(values[2].Raw) != "3" || !values[3].Missing {
		t.Fatalf("values = %+v", values)
	}
	if _, values, err := windows.Next(context.Background()); err != nil || len(values) != 0 {
		t.Fatalf("a read past the last key = %d values, %v; want none", len(values), err)
	}

	loading := answeredError("LOADING Redis is loading the dataset in memory")
	client = &fakeClient{values: map[string]string{"a": "1", "b": "2"}, answered: map[string]error{"a": loading, "b": loading}}
	if _, values, err := NewWindows(client, []string{"a", "b"}, 1<<20).Next(context.Background()); !errors.As(err, &unanswered) ||
		unanswered.Keys != 2 || values[0].Err == nil || values[1].Err == nil {
		t.Fatalf("a window Redis answered every key of with an error = %+v, %v", values, err)
	}

	client = &fakeClient{values: map[string]string{"a": "1"}, fail: errors.New("connection reset")}
	if _, values, err := NewWindows(client, []string{"a"}, 1<<20).Next(context.Background()); err == nil || values != nil ||
		errors.As(err, &unanswered) {
		t.Fatalf("a read whose transport failed = %d values, %v", len(values), err)
	}
}

// A code is the word Redis's error reply leads with, and only that word; a
// reply that leads with none is redis_error, and an error that is not Redis
// answering has no code.
func TestAnsweredCodeIsTheReplysLeadingWord(t *testing.T) {
	for _, test := range []struct {
		err      error
		code     string
		answered bool
	}{
		{err: answeredError("LOADING Redis is loading the dataset in memory"), code: "LOADING", answered: true},
		{err: fmt.Errorf("k: %w", answeredError("BUSY Redis is busy running a script")), code: "BUSY", answered: true},
		{err: answeredError("MASTERDOWN"), code: "MASTERDOWN", answered: true},
		{err: answeredError("oops at 192.0.2.1:6379"), code: "redis_error", answered: true},
		{err: errors.New("dial tcp 127.0.0.1:1: connect: connection refused")},
	} {
		if code, answered := AnsweredCode(test.err); code != test.code || answered != test.answered {
			t.Fatalf("%v = %q, %v; want %q, %v", test.err, code, answered, test.code, test.answered)
		}
	}
}
