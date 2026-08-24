// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package kafka

import (
	"context"
	"testing"

	"github.com/Shopify/sarama"
)

func TestGroupOffsetRepairerRepairsOnlyInvalidCommittedOffsets(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name          string
		initialOffset int64
		current       int64
		wantTarget    int64
		wantRepair    bool
	}{
		{name: "retention to oldest", initialOffset: sarama.OffsetOldest, current: 5, wantTarget: 10, wantRepair: true},
		{name: "retention to latest", initialOffset: sarama.OffsetNewest, current: 5, wantTarget: 20, wantRepair: true},
		{name: "log truncation", initialOffset: sarama.OffsetNewest, current: 25, wantTarget: 20, wantRepair: true},
		{name: "valid", initialOffset: sarama.OffsetNewest, current: 15},
		{name: "fresh group to latest", initialOffset: sarama.OffsetNewest, current: sarama.OffsetNewest, wantTarget: 20, wantRepair: true},
		{name: "fresh group to oldest", initialOffset: sarama.OffsetOldest, current: sarama.OffsetOldest, wantTarget: 10, wantRepair: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			partition := &fakeManagedPartitionOffset{current: test.current}
			manager := &fakeManagedGroupOffsets{partition: partition}
			var committed []OffsetReset
			repairer := &groupOffsetRepairer{
				client: &fakeOffsetRepairClient{oldest: 10, newest: 20},
				topics: []string{"detect-input"}, initialOffset: test.initialOffset,
				openOffsets: func() (managedGroupOffsets, error) { return manager, nil },
				commitOffsets: func(_ context.Context, offsets []OffsetReset) error {
					committed = append([]OffsetReset(nil), offsets...)
					return nil
				},
			}
			events, err := repairer.Repair(context.Background())
			if err != nil {
				t.Fatalf("Repair() error = %v", err)
			}
			if test.wantRepair {
				if len(events) != 1 || events[0].Topic != "detect-input" || events[0].Partition != 0 || events[0].Offset != test.wantTarget {
					t.Fatalf("Repair() events = %#v, want offset %d", events, test.wantTarget)
				}
				if len(committed) != 1 || committed[0].Offset != test.wantTarget {
					t.Fatalf("committed offsets = %#v, want %d", committed, test.wantTarget)
				}
			} else if len(events) != 0 || len(committed) != 0 {
				t.Fatalf("Repair() events=%#v committed=%#v, want unchanged", events, committed)
			}
			if !manager.closed {
				t.Fatal("Repair() did not close the offset manager")
			}
		})
	}
}

func TestGroupOffsetResetCommitterRequiresBrokerAck(t *testing.T) {
	t.Parallel()

	response := &sarama.OffsetCommitResponse{Errors: map[string]map[int32]sarama.KError{
		"detect-input": {0: sarama.ErrNoError},
	}}
	broker := &fakeOffsetBroker{response: response}
	committer := &groupOffsetResetCommitter{coordinator: fakeCoordinator{broker: broker}, groupID: "alarmd-shadow"}
	offsets := []OffsetReset{{Topic: "detect-input", Partition: 0, Offset: 20}}
	if err := committer.Commit(context.Background(), offsets); err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
	if broker.request.ConsumerGroupGeneration != sarama.GroupGenerationUndefined || broker.request.ConsumerID != "" {
		t.Fatalf("Commit() group identity = %#v", broker.request)
	}
	committed, _, err := broker.request.Offset("detect-input", 0)
	if err != nil || committed != 20 {
		t.Fatalf("Commit() offset=%d error=%v, want 20", committed, err)
	}

	broker.response.Errors["detect-input"][0] = sarama.ErrNotCoordinatorForConsumer
	if err := committer.Commit(context.Background(), offsets); err == nil {
		t.Fatal("Commit() accepted a broker rejection")
	}
}

type fakeOffsetRepairClient struct {
	oldest int64
	newest int64
}

func (c *fakeOffsetRepairClient) Partitions(string) ([]int32, error) { return []int32{0}, nil }

func (c *fakeOffsetRepairClient) GetOffset(_ string, _ int32, selector int64) (int64, error) {
	if selector == sarama.OffsetOldest {
		return c.oldest, nil
	}
	return c.newest, nil
}

type fakeManagedGroupOffsets struct {
	partition *fakeManagedPartitionOffset
	closed    bool
}

func (m *fakeManagedGroupOffsets) ManagePartition(string, int32) (sarama.PartitionOffsetManager, error) {
	return m.partition, nil
}

func (m *fakeManagedGroupOffsets) Close() error {
	m.closed = true
	return nil
}

type fakeManagedPartitionOffset struct {
	current int64
}

func (p *fakeManagedPartitionOffset) NextOffset() (int64, string) { return p.current, "" }
func (p *fakeManagedPartitionOffset) MarkOffset(offset int64, _ string) {
	if offset > p.current {
		p.current = offset
	}
}
func (p *fakeManagedPartitionOffset) ResetOffset(offset int64, _ string) {
	if offset <= p.current {
		p.current = offset
	}
}
func (p *fakeManagedPartitionOffset) Errors() <-chan *sarama.ConsumerError {
	return make(chan *sarama.ConsumerError)
}
func (p *fakeManagedPartitionOffset) AsyncClose()  {}
func (p *fakeManagedPartitionOffset) Close() error { return nil }
