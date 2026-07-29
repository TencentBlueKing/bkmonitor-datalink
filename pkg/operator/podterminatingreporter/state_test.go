// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package podterminatingreporter

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func testRow(seconds int64) MetricRow {
	return MetricRow{
		Namespace: "default",
		Pod:       "example",
		Node:      "node-1",
		Seconds:   seconds,
	}
}

func persistSnapshots(target *[]Snapshot) PersistFunc {
	return func(_ context.Context, snapshot Snapshot) (int, error) {
		*target = append(*target, snapshot)
		raw, err := MarshalSnapshot(snapshot)
		return len(raw), err
	}
}

func TestStateShortLivedDeletingPodIsContinuousTimeSeries(t *testing.T) {
	state := NewState(10*time.Minute, 3*time.Minute)
	now := time.Unix(1000, 0)
	var snapshots []Snapshot

	require.NoError(t, state.ApplySuccess(
		context.Background(),
		[]MetricRow{testRow(30)},
		now,
		persistSnapshots(&snapshots),
	))

	require.Equal(t, []MetricRow{testRow(30)}, state.MetricsSnapshot(now).Rows)
	require.Equal(t, []Dimension{{Namespace: "default", Pod: "example", Node: "node-1"}}, snapshots[0].Active)

	require.NoError(t, state.ApplySuccess(context.Background(), nil, now.Add(time.Minute), persistSnapshots(&snapshots)))
	require.Equal(t, []MetricRow{testRow(0)}, state.MetricsSnapshot(now.Add(time.Minute)).Rows)
}

func TestStateAllDeletingRowsRecoverAndExpire(t *testing.T) {
	state := NewState(10*time.Minute, 3*time.Minute)
	now := time.Unix(1000, 0)
	var snapshots []Snapshot

	err := state.ApplySuccess(context.Background(), []MetricRow{
		testRow(7199),
		{Namespace: "default", Pod: "equal", Node: "node-1", Seconds: 7200},
		{Namespace: "default", Pod: "above", Node: "node-1", Seconds: 7201},
	}, now, persistSnapshots(&snapshots))
	require.NoError(t, err)
	require.Equal(t, []Dimension{
		{Namespace: "default", Pod: "above", Node: "node-1"},
		{Namespace: "default", Pod: "equal", Node: "node-1"},
		{Namespace: "default", Pod: "example", Node: "node-1"},
	}, snapshots[0].Active)

	err = state.ApplySuccess(context.Background(), nil, now.Add(time.Minute), persistSnapshots(&snapshots))
	require.NoError(t, err)
	metrics := state.MetricsSnapshot(now.Add(time.Minute + time.Second))
	require.ElementsMatch(t, []MetricRow{
		{Namespace: "default", Pod: "example", Node: "node-1", Seconds: 0},
		{Namespace: "default", Pod: "equal", Node: "node-1", Seconds: 0},
		{Namespace: "default", Pod: "above", Node: "node-1", Seconds: 0},
	}, metrics.Rows)

	require.Empty(t, state.MetricsSnapshot(now.Add(11*time.Minute+time.Second)).Rows)
}

func TestStateShortLivedAndNodeReplacementKeepExactDimensions(t *testing.T) {
	state := NewState(10*time.Minute, 3*time.Minute)
	now := time.Unix(1000, 0)
	require.NoError(t, state.ApplySuccess(context.Background(), []MetricRow{testRow(30)}, now, nil))
	require.NoError(t, state.ApplySuccess(context.Background(), nil, now.Add(time.Minute), nil))
	require.Equal(t, []MetricRow{testRow(0)}, state.MetricsSnapshot(now.Add(time.Minute+time.Second)).Rows)

	require.NoError(t, state.ApplySuccess(context.Background(), []MetricRow{testRow(7200)}, now.Add(2*time.Minute), nil))
	replacement := MetricRow{Namespace: "default", Pod: "example", Node: "node-2", Seconds: 30}
	require.NoError(t, state.ApplySuccess(context.Background(), []MetricRow{replacement}, now.Add(3*time.Minute), nil))
	require.ElementsMatch(t, []MetricRow{
		{Namespace: "default", Pod: "example", Node: "node-1", Seconds: 0},
		replacement,
	}, state.MetricsSnapshot(now.Add(3*time.Minute+time.Second)).Rows)
}

func TestStateSameDimensionReappearanceReplacesRecovery(t *testing.T) {
	state := NewState(10*time.Minute, 3*time.Minute)
	now := time.Unix(1000, 0)
	require.NoError(t, state.ApplySuccess(context.Background(), []MetricRow{testRow(7200)}, now, nil))
	require.NoError(t, state.ApplySuccess(context.Background(), nil, now.Add(time.Minute), nil))

	require.NoError(t, state.ApplySuccess(context.Background(), []MetricRow{testRow(30)}, now.Add(2*time.Minute), nil))
	require.Equal(t, []MetricRow{testRow(30)}, state.MetricsSnapshot(now.Add(2*time.Minute+time.Second)).Rows)

	require.NoError(t, state.ApplySuccess(context.Background(), []MetricRow{testRow(7200)}, now.Add(3*time.Minute), nil))
	require.Equal(t, []MetricRow{testRow(7200)}, state.MetricsSnapshot(now.Add(3*time.Minute+time.Second)).Rows)
}

func TestStatePersistenceFailureDoesNotCommitNewSnapshot(t *testing.T) {
	state := NewState(10*time.Minute, 3*time.Minute)
	now := time.Unix(1000, 0)
	require.NoError(t, state.ApplySuccess(context.Background(), []MetricRow{testRow(7200)}, now, nil))

	expected := errors.New("state write failed")
	err := state.ApplySuccess(context.Background(), nil, now.Add(time.Minute), func(context.Context, Snapshot) (int, error) {
		return 0, expected
	})
	require.ErrorIs(t, err, expected)

	metrics := state.MetricsSnapshot(now.Add(time.Minute + time.Second))
	require.Equal(t, float64(1000), metrics.LastSuccessTimestamp)
	require.Equal(t, []MetricRow{testRow(7200)}, metrics.Rows)
}

func TestStateRestartExtensionIsPersistedOnce(t *testing.T) {
	state := NewState(10*time.Minute, 3*time.Minute)
	now := time.Unix(1060, 0)
	snapshot := Snapshot{
		Version: StateVersion,
		Recovery: []RecoveryDimension{{
			Dimension:            Dimension{Namespace: "default", Pod: "example", Node: "node-1"},
			ExpiresAt:            1050,
			RestartExtensionUsed: false,
		}},
	}
	raw, err := MarshalSnapshot(snapshot)
	require.NoError(t, err)
	require.NoError(t, state.Restore(snapshot, len(raw), now))

	var persisted []Snapshot
	require.NoError(t, state.ApplySuccess(context.Background(), nil, now, persistSnapshots(&persisted)))
	require.Len(t, persisted[0].Recovery, 1)
	require.True(t, persisted[0].Recovery[0].RestartExtensionUsed)
	require.Equal(t, float64(1660), persisted[0].Recovery[0].ExpiresAt)

	restarted := NewState(10*time.Minute, 3*time.Minute)
	raw, err = MarshalSnapshot(persisted[0])
	require.NoError(t, err)
	require.NoError(t, restarted.Restore(persisted[0], len(raw), time.Unix(1801, 0)))
	require.NoError(t, restarted.ApplySuccess(context.Background(), nil, time.Unix(1801, 0), nil))
	require.Empty(t, restarted.MetricsSnapshot(time.Unix(1802, 0)).Rows)
}

func TestStateRestartExtensionRetryKeepsDeadline(t *testing.T) {
	state := NewState(10*time.Minute, 3*time.Minute)
	snapshot := Snapshot{
		Version: StateVersion,
		Recovery: []RecoveryDimension{{
			Dimension: Dimension{Namespace: "default", Pod: "example", Node: "node-1"},
			ExpiresAt: 1500,
		}},
	}
	raw, err := MarshalSnapshot(snapshot)
	require.NoError(t, err)
	require.NoError(t, state.Restore(snapshot, len(raw), time.Unix(1200, 0)))

	var first Snapshot
	err = state.ApplySuccess(context.Background(), nil, time.Unix(1300, 0), func(_ context.Context, snapshot Snapshot) (int, error) {
		first = snapshot
		return 0, errors.New("timeout")
	})
	require.Error(t, err)

	var persisted []Snapshot
	require.NoError(t, state.ApplySuccess(context.Background(), nil, time.Unix(1400, 0), persistSnapshots(&persisted)))
	require.Equal(t, first.Recovery[0].ExpiresAt, persisted[0].Recovery[0].ExpiresAt)
}

func TestStateStaleSuppressesBusinessRowsAndReadiness(t *testing.T) {
	state := NewState(10*time.Minute, 3*time.Minute)
	now := time.Unix(1000, 0)
	require.False(t, state.IsFresh(now))
	require.NoError(t, state.ApplySuccess(context.Background(), []MetricRow{testRow(7200)}, now, nil))
	require.True(t, state.IsFresh(now.Add(3*time.Minute)))

	state.MarkFailure()
	fresh := state.MetricsSnapshot(now.Add(100 * time.Second))
	require.Equal(t, float64(0), fresh.RefreshSuccess)
	require.Equal(t, []MetricRow{testRow(7200)}, fresh.Rows)

	stale := state.MetricsSnapshot(now.Add(3*time.Minute + time.Second))
	require.Equal(t, float64(0), stale.RefreshSuccess)
	require.Empty(t, stale.Rows)
	require.False(t, state.IsFresh(now.Add(3*time.Minute+time.Second)))
}

func TestSnapshotStrictValidationAndDeterministicEncoding(t *testing.T) {
	valid := []byte(`{"version":2,"active":[],"recovery":[]}`)
	snapshot, err := UnmarshalSnapshot(valid, HardMaxStateBytes)
	require.NoError(t, err)
	require.Equal(t, StateVersion, snapshot.Version)

	fractionalExpiry := []byte(`{"version":2,"active":[],"recovery":[{"namespace":"n","pod":"p","node":"x","expires_at":1720000000.123456,"restart_extension_used":false}]}`)
	snapshot, err = UnmarshalSnapshot(fractionalExpiry, HardMaxStateBytes)
	require.NoError(t, err)
	require.InDelta(t, 1720000000.123456, snapshot.Recovery[0].ExpiresAt, 0.000001)

	for name, raw := range map[string]string{
		"wrong version":        `{"version":1,"active":[],"recovery":[]}`,
		"missing active":       `{"version":2,"recovery":[]}`,
		"null recovery":        `{"version":2,"active":[],"recovery":null}`,
		"unknown field":        `{"version":2,"active":[],"recovery":[],"extra":1}`,
		"missing dimension":    `{"version":2,"active":[{"namespace":"n","pod":"p"}],"recovery":[]}`,
		"non bool extension":   `{"version":2,"active":[],"recovery":[{"namespace":"n","pod":"p","node":"x","expires_at":1,"restart_extension_used":1}]}`,
		"trailing JSON object": `{"version":2,"active":[],"recovery":[]} {}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := UnmarshalSnapshot([]byte(raw), HardMaxStateBytes)
			require.Error(t, err)
		})
	}

	_, err = UnmarshalSnapshot(make([]byte, 11), 10)
	require.ErrorContains(t, err, "exceeds")
	_, err = UnmarshalSnapshot(valid, HardMaxStateBytes+1)
	require.ErrorContains(t, err, "hard limit")

	unsorted := Snapshot{
		Version: StateVersion,
		Active: []Dimension{
			{Namespace: "z", Pod: "b", Node: "n"},
			{Namespace: "a", Pod: "c", Node: "n"},
		},
	}
	first, err := MarshalSnapshot(unsorted)
	require.NoError(t, err)
	second, err := MarshalSnapshot(unsorted)
	require.NoError(t, err)
	require.Equal(t, first, second)
	require.Equal(t, `{"version":2,"active":[{"namespace":"a","pod":"c","node":"n"},{"namespace":"z","pod":"b","node":"n"}],"recovery":[]}`, string(first))
}

func BenchmarkStateApplySuccess4019Pods(b *testing.B) {
	rows := make([]MetricRow, 4019)
	for i := range rows {
		rows[i] = MetricRow{
			Namespace: "benchmark",
			Pod:       fmt.Sprintf("pod-%04d", i),
			Node:      fmt.Sprintf("node-%03d", i%100),
			Seconds:   30,
		}
	}
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		state := NewState(10*time.Minute, 3*time.Minute)
		err := state.ApplySuccess(ctx, rows, time.Unix(int64(i+1), 0), func(_ context.Context, snapshot Snapshot) (int, error) {
			raw, err := MarshalSnapshot(snapshot)
			return len(raw), err
		})
		if err != nil {
			b.Fatal(err)
		}
	}
}
