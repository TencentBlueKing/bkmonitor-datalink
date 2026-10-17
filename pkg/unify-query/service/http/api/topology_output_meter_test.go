// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb/v1beta3"
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

func outputMeterProbeItem() *cmdb.SharedTopologyResponseData {
	item := &cmdb.SharedTopologyResponseData{Code: 200, StartTime: 1, EndTime: 60, Step: "60s", PointCount: 60}
	label := strings.Repeat("a", 1200000)
	for i := 0; i < 60; i++ {
		item.Snapshots = append(item.Snapshots, cmdb.SharedTopologySnapshot{Timestamp: int64(i + 1), Nodes: []cmdb.SharedTopologyNode{{ID: 1, ResourceType: "node", Dimensions: cmdb.Matcher{"node": label}}}, Edges: []cmdb.SharedTopologyEdge{}})
	}
	return item
}

// Seed a previous response item through the actual CommitTopologyStage helper.
// Subsequent failure must keep this live item's materialized credit until release.
func outputMeterPriorItem(t *testing.T, ctx context.Context) (topologyEncodedOwnership, [][]byte) {
	t.Helper()
	item := cmdb.SharedTopologyResponseData{Code: 200, Snapshots: []cmdb.SharedTopologySnapshot{}}
	parts, size, owned, err := marshalTopologyBounded(ctx, item, 64*1024*1024, topologyEncodedOwnership{})
	require.NoError(t, err)
	require.NotEmpty(t, parts)
	require.Positive(t, size)
	require.Positive(t, owned.materialized)
	state := topologyTestAdmissionState(t)
	require.Equal(t, owned.materialized, state["materialized"])
	require.GreaterOrEqual(t, state["reserved"], owned.reserved)
	return owned, parts
}

func outputMeterAllReleased(t *testing.T) {
	t.Helper()
	require.Equal(t, map[string]int64{"active": 0, "waiters": 0, "reserved": 0, "materialized": 0}, topologyTestAdmissionState(t))
}

func TestOutputMeterMechanisms(t *testing.T) {
	log.InitTestLogger()
	metadata.InitMetadata()
	t.Run("dto-bound-and-standard-encoding", func(t *testing.T) {
		for _, s := range []string{"", "中文😀<&>\"\\\n\t", string([]byte{0xff, 0xfe}), "\u2028\u2029", strings.Repeat("界", 3000)} {
			n := cmdb.CompactTopologyNode{ID: "18446744073709551615", ResourceType: "pod", Dimensions: cmdb.Matcher{"key<&>": s}, Mask: []string{"0000000000000001"}}
			encoded, err := json.Marshal(n)
			require.NoError(t, err)
			size, err := topologyDTOEncoderBound(n)
			require.NoError(t, err)
			require.GreaterOrEqual(t, size, int64(len(encoded)))
			meter := &topologyEncodeMeter{ctx: context.Background(), limit: len(encoded)}
			actual, err := meter.marshal(n)
			require.NoError(t, err)
			require.Equal(t, encoded, actual)
			meter.limit--
			_, err = meter.marshal(n)
			require.ErrorContains(t, err, "max_topology_output_bytes")
		}
	})
	t.Run("dto-empty-and-numeric-boundaries", func(t *testing.T) {
		cases := []any{
			topologyResponseHeader{Code: -1, Start: -9223372036854775808, End: 9223372036854775807},
			topologySnapshotTrailer{Partial: false, Reason: ""},
			cmdb.CompactTopologyNode{ID: "", ResourceType: "", Dimensions: nil, Mask: nil},
		}
		for _, value := range cases {
			encoded, err := json.Marshal(value)
			require.NoError(t, err)
			size, err := topologyDTOEncoderBound(value)
			require.NoError(t, err)
			require.GreaterOrEqual(t, size, int64(len(encoded)))
			meter := &topologyEncodeMeter{ctx: context.Background(), limit: len(encoded)}
			actual, err := meter.marshal(value)
			require.NoError(t, err)
			require.Equal(t, encoded, actual)
			meter.limit--
			_, err = meter.marshal(value)
			require.ErrorContains(t, err, "max_topology_output_bytes")
		}
	})
	t.Run("actual-64MiB-wire-cap", func(t *testing.T) {
		ctx, release, err := v1beta3.AcquireSharedTopology(context.Background())
		require.NoError(t, err)
		defer release()
		prior, priorParts := outputMeterPriorItem(t, ctx)
		parts, size, held, err := marshalTopologyBounded(ctx, *outputMeterProbeItem(), 64*1024*1024, prior)
		require.ErrorContains(t, err, "max_topology_output_bytes")
		require.Nil(t, parts)
		require.Zero(t, size)
		require.Zero(t, held)
		state := topologyTestAdmissionState(t)
		require.Equal(t, prior.materialized, state["materialized"])
		require.GreaterOrEqual(t, state["reserved"], prior.reserved)
		runtime.KeepAlive(priorParts)
		release()
		outputMeterAllReleased(t)
		fmt.Printf("OUTPUT_METER_BOUNDARY actual64MiB rejected before full item; prior=%+v before_release=%v all_four=0\n", prior, state)
	})
	t.Run("cancel-during-encoding", func(t *testing.T) {
		base, release, err := v1beta3.AcquireSharedTopology(context.Background())
		require.NoError(t, err)
		defer release()
		prior, priorParts := outputMeterPriorItem(t, base)
		ctx := &outputMeterCancelContext{Context: base, after: 100, done: make(chan struct{})}
		_, _, _, err = marshalTopologyBounded(ctx, *outputMeterProbeItem(), 64*1024*1024, prior)
		require.True(t, errors.Is(err, context.Canceled), err)
		state := topologyTestAdmissionState(t)
		require.Equal(t, prior.materialized, state["materialized"])
		require.GreaterOrEqual(t, state["reserved"], prior.reserved)
		runtime.KeepAlive(priorParts)
		release()
		outputMeterAllReleased(t)
		fmt.Printf("OUTPUT_METER_BOUNDARY during-encode cancellation; prior=%+v before_release=%v all_four=0\n", prior, state)
	})
	t.Run("low-memory-preallocation", func(t *testing.T) {
		oldLimit := v1beta3.MaxSharedTopologyReservedBytes
		defer func() {
			v1beta3.MaxSharedTopologyReservedBytes = oldLimit
		}()
		v1beta3.MaxSharedTopologyReservedBytes = 1024 * 1024
		ctx, release, err := v1beta3.AcquireSharedTopology(context.Background())
		require.NoError(t, err)
		defer release()
		item := cmdb.SharedTopologyResponseData{Code: 200, Snapshots: []cmdb.SharedTopologySnapshot{}}
		_, _, _, err = marshalTopologyBounded(ctx, item, 64*1024*1024, topologyEncodedOwnership{})
		require.ErrorContains(t, err, "max_topology_reserved_bytes")
		require.Zero(t, topologyTestAdmissionState(t)["reserved"])
		require.Zero(t, topologyTestAdmissionState(t)["materialized"])
		release()
		outputMeterAllReleased(t)
		fmt.Println("OUTPUT_METER_BOUNDARY 1MiB budget failed before 4MiB encoder reservation; all_four=0")
	})
	t.Run("real-commit-prior-item-and-stage-clear", func(t *testing.T) {
		ctx, release, err := v1beta3.AcquireSharedTopology(context.Background())
		require.NoError(t, err)
		defer release()
		prior, priorParts := outputMeterPriorItem(t, ctx)
		item := cmdb.SharedTopologyResponseData{Code: 200, Snapshots: []cmdb.SharedTopologySnapshot{}}
		parts, size, owned, err := marshalTopologyBounded(ctx, item, 64*1024*1024, prior)
		require.NoError(t, err)
		require.NotEmpty(t, parts)
		require.Positive(t, size)
		state := topologyTestAdmissionState(t)
		require.Equal(t, prior.materialized+owned.materialized, state["materialized"])
		require.Equal(t, prior.reserved+owned.reserved, state["reserved"])
		runtime.KeepAlive(priorParts)
		runtime.KeepAlive(parts)
		require.NoError(t, v1beta3.ReserveTopologyStage(ctx, "encoded", 0))
		require.Zero(t, topologyTestAdmissionState(t)["reserved"])
		require.Zero(t, topologyTestAdmissionState(t)["materialized"])
		release()
		outputMeterAllReleased(t)
		fmt.Printf("OUTPUT_METER_BOUNDARY real Commit retained two items; before_clear=%v all_four=0\n", state)
	})
}
