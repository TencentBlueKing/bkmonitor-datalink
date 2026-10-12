// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package controlplane_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"testing"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
)

// mgetRecorder counts the keys of every MGET the source sends, and answers the
// call numbered by fail (from 1) with what fail returns instead of the store.
type mgetRecorder struct {
	redis.Cmdable
	keys []int
	fail func(call int, keys []string) *redis.SliceCmd
}

func (r *mgetRecorder) MGet(ctx context.Context, keys ...string) *redis.SliceCmd {
	r.keys = append(r.keys, len(keys))
	if r.fail != nil {
		if cmd := r.fail(len(r.keys), keys); cmd != nil {
			return cmd
		}
	}
	return r.Cmdable.MGet(ctx, keys...)
}

// storeStrategyDocuments writes n minimal documents, ids 1..n, and returns
// the ids from the last to the first: an order the store does not sort, so a
// document landing on the wrong id shows.
func storeStrategyDocuments(t *testing.T, client *redis.Client, n int) []string {
	t.Helper()
	ctx := context.Background()
	ids := make([]string, 0, n)
	_, err := client.Pipelined(ctx, func(pipe redis.Pipeliner) error {
		for id := n; id >= 1; id-- {
			ids = append(ids, strconv.Itoa(id))
			pipe.Set(ctx, "bkmonitor.cache.strategy_"+strconv.Itoa(id),
				fmt.Sprintf(`{"id":%d,"bk_biz_id":2,"bk_tenant_id":"system","space_uid":"bkcc__2"}`, id), 0)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return ids
}

// A read of as many documents as one MGET takes is one MGET; one more is a
// second MGET of the one left, and every document still lands on its own id
// across the boundary. No documents is no MGET.
func TestLegacyRedisStrategySourceReadsDocumentsInBoundedMGets(t *testing.T) {
	chunk := controlplane.LegacyStrategyMGetChunkForTest
	// The cases below follow the constant, so they pass whatever it is set
	// to - a value large enough to put every document back in one MGET
	// included. The bound itself is the decision, and it is held here.
	if chunk != 500 {
		t.Fatalf("one MGET reads %d strategy documents, want 500", chunk)
	}
	for _, tc := range []struct {
		n    int
		want []int
	}{
		{n: 0, want: nil},
		{n: chunk, want: []int{chunk}},
		{n: chunk + 1, want: []int{chunk, 1}},
	} {
		t.Run(strconv.Itoa(tc.n), func(t *testing.T) {
			client := newControlplaneRedis(t)
			ids := storeStrategyDocuments(t, client, tc.n)
			recorder := &mgetRecorder{Cmdable: client}
			source, err := controlplane.NewLegacyRedisStrategySource(recorder, "bkmonitor.cache")
			if err != nil {
				t.Fatal(err)
			}
			strategies, err := source.Strategies(context.Background(), ids)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(recorder.keys, tc.want) {
				t.Fatalf("MGETs of %v keys for %d documents, want %v", recorder.keys, tc.n, tc.want)
			}
			if len(strategies) != len(ids) {
				t.Fatalf("%d strategies for %d ids", len(strategies), len(ids))
			}
			for index, strategy := range strategies {
				if strategy.SourceID != ids[index] || strategy.SourceDisposition != nil || strategy.Identity.BusinessID != "2" {
					t.Fatalf("strategy %d = %s %+v, want id %s read cleanly", index, strategy.SourceID, strategy.SourceDisposition, ids[index])
				}
			}
		})
	}
}

// A chunk that fails fails the whole read, with nothing returned from the
// chunks before it: the round keeps what it had rather than a part of this
// one. A chunk that comes back short is the unstable observation it always
// was, whichever chunk it is.
func TestLegacyRedisStrategySourceFailsTheWholeReadOnAnyChunk(t *testing.T) {
	chunk := controlplane.LegacyStrategyMGetChunkForTest
	client := newControlplaneRedis(t)
	ids := storeStrategyDocuments(t, client, chunk+1)

	refused := errors.New("dial tcp 127.0.0.1:6379: connect: connection refused")
	failing := &mgetRecorder{Cmdable: client, fail: func(call int, _ []string) *redis.SliceCmd {
		if call == 2 {
			return redis.NewSliceResult(nil, refused)
		}
		return nil
	}}
	source, err := controlplane.NewLegacyRedisStrategySource(failing, "bkmonitor.cache")
	if err != nil {
		t.Fatal(err)
	}
	strategies, err := source.Strategies(context.Background(), ids)
	if !errors.Is(err, refused) || strategies != nil || !reflect.DeepEqual(failing.keys, []int{chunk, 1}) {
		t.Fatalf("second chunk refused = %d strategies, %v after MGETs of %v; want the refusal and nothing", len(strategies), err, failing.keys)
	}

	short := &mgetRecorder{Cmdable: client, fail: func(call int, keys []string) *redis.SliceCmd {
		if call == 2 {
			return redis.NewSliceResult([]interface{}{}, nil)
		}
		return nil
	}}
	source, err = controlplane.NewLegacyRedisStrategySource(short, "bkmonitor.cache")
	if err != nil {
		t.Fatal(err)
	}
	strategies, err = source.Strategies(context.Background(), ids)
	if !errors.Is(err, controlplane.ErrObservationUnstable) || strategies != nil {
		t.Fatalf("second chunk short = %d strategies, %v; want an unstable observation", len(strategies), err)
	}
}

// scribblingMGet answers each MGET with the documents as byte slices it
// keeps, and overwrites the previous answer's slices when the next MGET is
// sent: a read that still held a chunk's replies when it read the next one
// would turn documents it had not copied yet into garbage.
type scribblingMGet struct {
	redis.Cmdable
	previous [][]byte
	calls    int
}

func (r *scribblingMGet) MGet(ctx context.Context, keys ...string) *redis.SliceCmd {
	for _, value := range r.previous {
		for index := range value {
			value[index] = 'x'
		}
	}
	r.calls++
	values, err := r.Cmdable.MGet(ctx, keys...).Result()
	if err != nil {
		return redis.NewSliceResult(nil, err)
	}
	r.previous = r.previous[:0]
	answer := make([]interface{}, len(values))
	for index, value := range values {
		if text, ok := value.(string); ok {
			payload := []byte(text)
			r.previous = append(r.previous, payload)
			answer[index] = payload
		}
	}
	return redis.NewSliceResult(answer, nil)
}

// Each chunk of replies is turned into its strategies before the next chunk
// is read: the read holds one chunk of replies besides the documents, at two
// chunks and at four alike. A chunk still held when the next was read would
// come out overwritten.
func TestLegacyRedisStrategySourceTurnsEachChunkIntoDocumentsBeforeTheNext(t *testing.T) {
	chunk := controlplane.LegacyStrategyMGetChunkForTest
	for _, n := range []int{2 * chunk, 4 * chunk} {
		client := newControlplaneRedis(t)
		ids := storeStrategyDocuments(t, client, n)
		scribbling := &scribblingMGet{Cmdable: client}
		source, err := controlplane.NewLegacyRedisStrategySource(scribbling, "bkmonitor.cache")
		if err != nil {
			t.Fatal(err)
		}
		strategies, err := source.Strategies(context.Background(), ids)
		if err != nil || len(strategies) != n || scribbling.calls != n/chunk {
			t.Fatalf("%d documents: %d strategies over %d MGETs, %v", n, len(strategies), scribbling.calls, err)
		}
		for index, strategy := range strategies {
			want := fmt.Sprintf(`{"id":%s,"bk_biz_id":2,"bk_tenant_id":"system","space_uid":"bkcc__2"}`, ids[index])
			if strategy.SourceDisposition != nil || string(strategy.Document) != want {
				t.Fatalf("%d documents: strategy %s = %q (%+v), want its document intact", n, ids[index], strategy.Document, strategy.SourceDisposition)
			}
		}
	}
}
