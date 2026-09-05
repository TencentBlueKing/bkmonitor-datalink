// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package controlplane

import (
	"context"
	"runtime"
	"strings"
	"testing"

	"github.com/go-redis/redis/v8"
)

type snapshotPayloadClient struct {
	redis.Cmdable
	payload interface{}
}

func (client snapshotPayloadClient) MGet(context.Context, ...string) *redis.SliceCmd {
	return redis.NewSliceResult([]interface{}{client.payload, "1"}, nil)
}

func TestSnapshotWarmReadDoesNotCopyCompletePayload(t *testing.T) {
	revision, raw := neutralSnapshotPayload(t, "qg-a")
	payload := strings.Repeat(" ", 1<<20) + string(raw)
	client := &snapshotPayloadClient{payload: payload}
	repository, err := NewRedisCatalogRepository(client, "test", 1)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	first, _, err := repository.loadSnapshotPayload(ctx, revision)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := repository.snapshotCache.load(ctx, revision, first); err != nil {
		t.Fatal(err)
	}
	// The next Redis result has equal bytes on independent backing storage.
	client.payload = strings.Clone(payload)
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	const reads = 20
	for i := 0; i < reads; i++ {
		loaded, _, err := repository.loadSnapshotPayload(ctx, revision)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := repository.snapshotCache.load(ctx, revision, loaded); err != nil {
			t.Fatal(err)
		}
	}
	runtime.ReadMemStats(&after)
	allocated := after.TotalAlloc - before.TotalAlloc
	t.Logf("warm reads=%d allocated=%d payload=%d", reads, allocated, len(payload))
	if allocated >= uint64(len(payload)/2) {
		t.Fatalf("warm payload reads copied complete content: allocated=%d payload=%d reads=%d", allocated, len(payload), reads)
	}
}

func TestSnapshotBytePayloadFallbackOwnsImmutableContent(t *testing.T) {
	revision, encoded := neutralSnapshotPayload(t, "qg-a")
	raw := []byte(encoded)
	original := string(raw)
	client := &snapshotPayloadClient{payload: raw}
	repository, err := NewRedisCatalogRepository(client, "test", 1)
	if err != nil {
		t.Fatal(err)
	}
	payload, _, err := repository.loadSnapshotPayload(context.Background(), revision)
	if err != nil {
		t.Fatal(err)
	}
	raw[0] = '!'
	if string(payload) != original {
		t.Fatal("loaded Snapshot aliases mutable client bytes")
	}
}
