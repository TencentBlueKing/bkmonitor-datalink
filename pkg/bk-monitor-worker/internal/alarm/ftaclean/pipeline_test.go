// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package ftaclean

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Shopify/sarama"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/require"

	tc "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
)

func fixture(tb testing.TB, s slots, n int) (*pipeline, []*sarama.ConsumerMessage) {
	tb.Helper()
	spec, msgs := fixtureSpec(tb, n)
	p, err := compile(&spec, s)
	require.NoError(tb, err)
	return p, msgs
}

func fixtureSpec(tb testing.TB, n int) (tc.PipelineConfig, []*sarama.ConsumerMessage) {
	tb.Helper()
	fields := []*tc.MetaFieldConfig{{FieldName: "alert_name", Type: "string"}, {FieldName: "time", Type: "timestamp"}, {FieldName: "severity", Type: "int"}}
	rules := []map[string]string{{"field": "alert_name", "expr": "name"}, {"field": "time", "expr": "time"}, {"field": "severity", "expr": "level"}, {"field": "tags", "expr": "labels"}, {"field": "dimensions", "expr": "{host: host}"}}
	var tables []*tc.MetaResultTableConfig
	for _, topic := range []string{"alerts", "mirror"} {
		ship := &tc.MetaClusterInfo{ClusterType: "kafka", ClusterConfig: map[string]any{"domain_name": "test", "port": 9092, "cluster_id": 7}, StorageConfig: map[string]any{"topic": topic}}
		tables = append(tables, &tc.MetaResultTableConfig{ResultTable: topic, FieldList: fields, ShipperList: []*tc.MetaClusterInfo{ship}})
	}
	spec := tc.PipelineConfig{DataID: 42, ETLConfig: "bk_fta_event", Option: map[string]any{"normalization_config": rules}, ResultTableList: tables, MQConfig: tables[0].ShipperList[0]}
	events := make([]map[string]any, n)
	for i := range events {
		events[i] = map[string]any{"name": fmt.Sprintf("alert-%d", i), "time": 1700000000, "level": 2, "host": "demo", "labels": map[string]string{"source": "fixture"}}
	}
	raw, err := json.Marshal(map[string]any{"data": events, "__bk_event_id__": "fallback", "bk_plugin_id": "test", "bk_ingest_time": 1700000001})
	require.NoError(tb, err)
	return spec, []*sarama.ConsumerMessage{{Value: raw}}
}

func TestPipeline(t *testing.T) {
	p, msgs := fixture(t, make(slots, 4), 65)
	msgs = append(msgs, &sarama.ConsumerMessage{Value: []byte("invalid")}, &sarama.ConsumerMessage{Value: []byte(`{"data":[42,{}]}`)})
	out, err := p.clean(context.Background(), msgs)
	require.NoError(t, err)
	require.Len(t, out[7], 130)
	for i, msg := range out[7] {
		value, _ := msg.Value.Encode()
		var row map[string]any
		require.NoError(t, json.Unmarshal(value, &row))
		require.Equal(t, []string{"alerts", "mirror"}[i%2], msg.Topic)
		require.Equal(t, fmt.Sprintf("alert-%d", i/2), row["alert_name"])
		require.Equal(t, "fallback", row["event_id"])
		require.Equal(t, "test", row["plugin_id"])
		require.Equal(t, 1700000000., row["time"])
		require.Equal(t, []any{"tags.host"}, row["dedupe_keys"])
		require.Len(t, row["tags"], 2)
	}
}

func TestSharedLimit(t *testing.T) {
	for _, limit := range []int{1, 3, 8} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			s := make(slots, limit)
			var peak, current int
			var mu sync.Mutex
			var callers sync.WaitGroup
			ready := make(chan struct{}, limit)
			release := make(chan struct{})
			for i := 0; i < 4; i++ {
				callers.Add(1)
				go func() {
					defer callers.Done()
					s.run(context.Background(), 32, 1, slotWait.WithLabelValues("42"), func(int, int) {
						mu.Lock()
						current++
						peak = max(peak, current)
						mu.Unlock()
						select {
						case ready <- struct{}{}:
						default:
						}
						<-release
						mu.Lock()
						current--
						mu.Unlock()
					})
				}()
			}
			for i := 0; i < limit; i++ {
				select {
				case <-ready:
				case <-time.After(time.Second):
					t.Fatal("concurrency did not reach limit")
				}
			}
			close(release)
			callers.Wait()
			require.EqualValues(t, limit, peak)
		})
	}
}

func TestPartitionBatchBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name        string
		count, size int
		wait        time.Duration
		messages    int
	}{{"count", 2, 100, time.Hour, 2}, {"bytes", 10, 2, time.Hour, 2}, {"deadline", 10, 100, time.Millisecond, 1}} {
		t.Run(tc.name, func(t *testing.T) {
			input := make(chan *sarama.ConsumerMessage, tc.messages)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			for i := 0; i < tc.messages; i++ {
				input <- &sarama.ConsumerMessage{Offset: int64(10 + i), Value: []byte{1}}
			}
			marked := make(chan int64, 1)
			done := make(chan error, 1)
			go func() {
				done <- consumeBatches(ctx, input, tc.count, tc.size, tc.wait, func(batch []*sarama.ConsumerMessage) error { return nil }, func(m *sarama.ConsumerMessage) { marked <- m.Offset })
			}()
			select {
			case mark := <-marked:
				require.EqualValues(t, 9+tc.messages, mark)
			case <-time.After(time.Second):
				t.Fatal("batch was not acknowledged")
			}
			cancel()
			require.ErrorIs(t, <-done, context.Canceled)
		})
	}
}

func TestWriteFailureAndRebalance(t *testing.T) {
	for _, rebalance := range []bool{true, false} {
		ctx, cancel := context.WithCancel(context.Background())
		input := make(chan *sarama.ConsumerMessage, 2)
		input <- &sarama.ConsumerMessage{Offset: 1}
		input <- &sarama.ConsumerMessage{Offset: 2}
		close(input)
		var marked []int64
		err := consumeBatches(ctx, input, 1, 100, time.Hour, func(batch []*sarama.ConsumerMessage) error {
			if batch[0].Offset == 2 {
				if rebalance {
					cancel()
				} else {
					return errors.New("partial write")
				}
			}
			return nil
		}, func(m *sarama.ConsumerMessage) { marked = append(marked, m.Offset) })
		cancel()
		require.Error(t, err)
		require.Equal(t, []int64{1}, marked)
	}
}

func TestBlockedPartition(t *testing.T) {
	p, msgs := fixture(t, make(slots, 1), 4)
	blocked := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 2)
	run := func(slow bool) {
		input := make(chan *sarama.ConsumerMessage, 1)
		input <- msgs[0]
		close(input)
		done <- consumeBatches(context.Background(), input, 1, 128, time.Second, func(batch []*sarama.ConsumerMessage) error {
			_, err := p.clean(context.Background(), batch)
			if slow {
				close(blocked)
				<-release
			}
			return err
		}, func(*sarama.ConsumerMessage) {})
	}
	go run(true)
	<-blocked
	go run(false)
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("slow output blocked another partition")
	}
	close(release)
	require.NoError(t, <-done)
}

func TestUpdateRules(t *testing.T) {
	spec, msgs := fixtureSpec(t, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan string, 2)
	run := func(ctx context.Context, _ *tc.PipelineConfig, p *pipeline) error {
		out, err := p.clean(ctx, msgs)
		if err != nil {
			return err
		}
		value, _ := out[7][0].Value.Encode()
		var row map[string]any
		json.Unmarshal(value, &row)
		ready <- row["alert_name"].(string)
		<-ctx.Done()
		return ctx.Err()
	}
	active := map[uint]*consumer{}
	capacity := make(slots, 2)
	apply := func() error {
		raw, err := json.Marshal(spec)
		require.NoError(t, err)
		return update(ctx, active, 42, raw, capacity, run)
	}
	require.NoError(t, apply())
	first := active[42]
	require.Equal(t, "alert-0", <-ready)
	require.NoError(t, apply())
	require.Same(t, first, active[42])
	spec.Option["normalization_config"] = []map[string]string{{"field": "alert_name", "expr": "["}}
	require.Error(t, apply())
	require.Same(t, first, active[42])
	spec.Option["normalization_config"] = []map[string]string{{"field": "alert_name", "expr": "'updated'"}}
	require.NoError(t, apply())
	require.Equal(t, "updated", <-ready)
	select {
	case <-first.done:
	default:
		t.Fatal("old consumer still active after reload")
	}
	last := active[42]
	spec.ResultTableList = nil
	require.NoError(t, apply())
	require.Empty(t, active)
	<-last.done
}

func TestInvalidInputMetrics(t *testing.T) {
	p, msgs := fixture(t, make(slots, 2), 3)
	p.id = 901
	errors := discarded.WithLabelValues("901", "decode")
	events := expanded.WithLabelValues("901")
	var beforeErrors, afterErrors, beforeEvents, afterEvents dto.Metric
	require.NoError(t, errors.Write(&beforeErrors))
	require.NoError(t, events.Write(&beforeEvents))
	msgs = append(msgs, &sarama.ConsumerMessage{Value: []byte("invalid")})
	_, err := p.clean(context.Background(), msgs)
	require.NoError(t, err)
	require.NoError(t, errors.Write(&afterErrors))
	require.NoError(t, events.Write(&afterEvents))
	require.Equal(t, 1., afterErrors.GetCounter().GetValue()-beforeErrors.GetCounter().GetValue())
	require.Equal(t, 3., afterEvents.GetCounter().GetValue()-beforeEvents.GetCounter().GetValue())
	var used dto.Metric
	require.NoError(t, slotsUsed.Write(&used))
	require.Zero(t, used.GetGauge().GetValue())
}
