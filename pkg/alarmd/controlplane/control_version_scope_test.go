// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package controlplane

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
)

// Only the one version read is exercised, so embedding the interface is enough:
// any other call would panic and thereby prove the test reached code it should
// not. That guard did its job when the read became a pipeline - the fake
// answered a plain GET and nothing else, so the change could not pass silently.
//
// reads counts round trips, which is what these tests are about. The version
// read now carries the activation's length alongside the header, so one round
// trip answers two commands; counting commands instead would report two for a
// read that costs one.
type countingHeaderClient struct {
	redis.Cmdable
	header     string
	activation string
	reads      int
	err        error
}

type headerPipeline struct {
	redis.Pipeliner
	client *countingHeaderClient
}

func (pipe *headerPipeline) Get(context.Context, string) *redis.StringCmd {
	if pipe.client.err != nil {
		return redis.NewStringResult("", pipe.client.err)
	}
	return redis.NewStringResult(pipe.client.header, nil)
}

func (pipe *headerPipeline) StrLen(context.Context, string) *redis.IntCmd {
	if pipe.client.err != nil {
		return redis.NewIntResult(0, pipe.client.err)
	}
	return redis.NewIntResult(int64(len(pipe.client.activation)), nil)
}

func (client *countingHeaderClient) Pipelined(
	_ context.Context,
	fn func(redis.Pipeliner) error,
) ([]redis.Cmder, error) {
	client.reads++
	if err := fn(&headerPipeline{client: client}); err != nil {
		return nil, err
	}
	return nil, client.err
}

func newScopeRepository(t *testing.T, client *countingHeaderClient) *RedisCatalogRepository {
	t.Helper()
	repository, err := NewRedisCatalogRepository(client, "alarmd:test", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return repository
}

func TestControlVersionScopeReadsHeaderOncePerOperation(t *testing.T) {
	client := &countingHeaderClient{header: "v1", activation: "{}"}
	repository := newScopeRepository(t, client)
	ctx := WithControlVersionScope(context.Background())
	for i := 0; i < 10; i++ {
		version, err := repository.readControlVersion(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if version.header != "v1" || !version.known {
			t.Fatalf("unexpected version %+v", version)
		}
	}
	if client.reads != 1 {
		t.Fatalf("10 reads in one scope performed %d header reads, want 1", client.reads)
	}
	stats := repository.ControlReadCacheStats()
	if stats.Version.Hits != 9 || stats.Version.Misses != 1 {
		t.Fatalf("version counters = %+v", stats.Version)
	}
}

// The whole point of scoping rather than caching: a new operation must observe
// a cutover, because Segment closure is decided by seeing the header change.
func TestControlVersionScopeObservesCutoverInTheNextOperation(t *testing.T) {
	client := &countingHeaderClient{header: "v1", activation: "{}"}
	repository := newScopeRepository(t, client)
	first, err := repository.readControlVersion(WithControlVersionScope(context.Background()))
	if err != nil {
		t.Fatal(err)
	}
	client.header = "v2"
	second, err := repository.readControlVersion(WithControlVersionScope(context.Background()))
	if err != nil {
		t.Fatal(err)
	}
	if first.header != "v1" || second.header != "v2" {
		t.Fatalf("cutover was not observed: first=%+v second=%+v", first, second)
	}
}

func TestControlVersionScopeAbsentKeepsEveryReadLive(t *testing.T) {
	client := &countingHeaderClient{header: "v1", activation: "{}"}
	repository := newScopeRepository(t, client)
	for i := 0; i < 5; i++ {
		if _, err := repository.readControlVersion(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if client.reads != 5 {
		t.Fatalf("reads = %d, want 5 without a scope", client.reads)
	}
}

// Nesting must not narrow an outer scope, or an inner call would start reading
// the header again and undo the saving.
func TestControlVersionScopeNestingIsIdempotent(t *testing.T) {
	client := &countingHeaderClient{header: "v1", activation: "{}"}
	repository := newScopeRepository(t, client)
	outer := WithControlVersionScope(context.Background())
	inner := WithControlVersionScope(outer)
	if _, err := repository.readControlVersion(outer); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.readControlVersion(inner); err != nil {
		t.Fatal(err)
	}
	if client.reads != 1 {
		t.Fatalf("nested scope performed %d reads, want 1", client.reads)
	}
}

// A failed read is not remembered, so the next call in the same operation
// retries instead of inheriting the failure.
func TestControlVersionScopeDoesNotRememberFailures(t *testing.T) {
	client := &countingHeaderClient{header: "v1", activation: "{}", err: errors.New("redis unavailable")}
	repository := newScopeRepository(t, client)
	ctx := WithControlVersionScope(context.Background())
	if _, err := repository.readControlVersion(ctx); err == nil {
		t.Fatal("expected the read error to surface")
	}
	client.err = nil
	version, err := repository.readControlVersion(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if version.header != "v1" {
		t.Fatalf("retry did not reach Redis: %+v", version)
	}
	if client.reads != 2 {
		t.Fatalf("reads = %d, want 2", client.reads)
	}
}
