// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package api

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/stretchr/testify/require"
)

type outputMeterCancelContext struct {
	context.Context
	n     atomic.Int64
	after int64
	done  chan struct{}
	once  sync.Once
}

func (c *outputMeterCancelContext) Err() error {
	if c.n.Add(1) > c.after {
		c.once.Do(func() { close(c.done) })
		return context.Canceled
	}
	return nil
}
func (c *outputMeterCancelContext) Done() <-chan struct{} { return c.done }

// 60 帧各含一个 1.2MB 属性的节点，编码后超过 64 MiB。
func outputMeterProbeItem() *cmdb.SharedTopologyResponseData {
	item := &cmdb.SharedTopologyResponseData{Code: 200, StartTime: 1, EndTime: 60, Step: "60s", PointCount: 60}
	label := strings.Repeat("a", 1200000)
	for i := 0; i < 60; i++ {
		item.Snapshots = append(item.Snapshots, cmdb.SharedTopologySnapshot{Timestamp: int64(i + 1), Nodes: []cmdb.SharedTopologyNode{{ID: 1, ResourceType: "node", Dimensions: cmdb.Matcher{"node": label}}}, Edges: []cmdb.SharedTopologyEdge{}})
	}
	return item
}

func TestOutputMeterMechanisms(t *testing.T) {
	log.InitTestLogger()
	metadata.InitMetadata()
	t.Run("dto-bound-and-standard-encoding", func(t *testing.T) {
		for _, s := range []string{"", "中文😀<&>\"\\\n\t", string([]byte{0xff, 0xfe}), "  ", strings.Repeat("界", 3000)} {
			n := cmdb.CompactTopologyNode{ID: "18446744073709551615", ResourceType: "pod", Dimensions: cmdb.Matcher{"key<&>": s}, Mask: []string{"0000000000000001"}}
			encoded, err := json.Marshal(n)
			require.NoError(t, err)
			size, err := topologyDTOEncoderBound(n)
			require.NoError(t, err)
			require.GreaterOrEqual(t, size, int64(len(encoded)))
			meter := &topologyEncodeMeter{ctx: context.Background()}
			actual, err := meter.marshal(n)
			require.NoError(t, err)
			require.Equal(t, encoded, actual)
		}
	})
	t.Run("dto-empty-and-numeric-boundaries", func(t *testing.T) {
		cases := []any{
			cmdb.CompactTopologyNode{ID: "", ResourceType: "", Dimensions: nil, Mask: nil},
			cmdb.CompactTopologyEdge{},
			[]cmdb.CompactTopologyPartial{},
			[]cmdb.CompactTopologyPartial{{Index: 59, Reason: "relation_no_physical_route:a;relation_no_physical_route:b"}},
		}
		for _, value := range cases {
			encoded, err := json.Marshal(value)
			require.NoError(t, err)
			size, err := topologyDTOEncoderBound(value)
			require.NoError(t, err)
			require.GreaterOrEqual(t, size, int64(len(encoded)))
			meter := &topologyEncodeMeter{ctx: context.Background()}
			actual, err := meter.marshal(value)
			require.NoError(t, err)
			require.Equal(t, encoded, actual)
		}
	})
	// 编码字节不是拒绝理由：超过旧的 64 MiB 输出上限的项完整返回。
	t.Run("beyond-former-64MiB-cap-encodes-completely", func(t *testing.T) {
		item := *outputMeterProbeItem()
		parts, size, err := marshalTopologyItem(context.Background(), item)
		require.NoError(t, err)
		want, err := json.Marshal(item)
		require.NoError(t, err)
		require.Greater(t, len(want), 64*1024*1024)
		require.Equal(t, len(want), size)
		hash := sha256.New()
		for _, part := range parts {
			hash.Write(part)
		}
		require.Equal(t, sha256.Sum256(want), [sha256.Size]byte(hash.Sum(nil)))
	})
	t.Run("cancel-during-encoding", func(t *testing.T) {
		ctx := &outputMeterCancelContext{Context: context.Background(), after: 100, done: make(chan struct{})}
		parts, size, err := marshalTopologyItem(ctx, *outputMeterProbeItem())
		require.True(t, errors.Is(err, context.Canceled), err)
		require.Nil(t, parts)
		require.Zero(t, size)
	})
}
