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
	"errors"
	"testing"

	"github.com/Shopify/sarama"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/consumer"
)

func TestSaramaOffsetCommitterCommitsNextOffsetForCurrentGeneration(t *testing.T) {
	t.Parallel()

	broker := &fakeOffsetBroker{}
	committer := &SaramaOffsetCommitter{coordinator: fakeCoordinator{broker: broker}, groupID: "alarmd-shadow"}
	session := newFakeSession(context.Background(), &[]string{})
	record := consumer.Record{Topic: "trigger-input", Partition: 3, Offset: 41}
	if err := committer.CommitOffset(context.Background(), session, record); err != nil {
		t.Fatalf("CommitOffset() error = %v", err)
	}
	if broker.request == nil {
		t.Fatal("broker did not receive offset commit request")
	}
	if broker.request.ConsumerGroup != "alarmd-shadow" || broker.request.ConsumerGroupGeneration != 1 || broker.request.ConsumerID != "member" {
		t.Fatalf("request group identity = %#v", broker.request)
	}
	offset, _, err := broker.request.Offset("trigger-input", 3)
	if err != nil {
		t.Fatalf("read committed offset: %v", err)
	}
	if offset != 42 {
		t.Fatalf("committed offset = %d, want 42", offset)
	}
}

func TestSaramaOffsetCommitterReturnsBrokerAndResponseErrors(t *testing.T) {
	t.Parallel()

	want := errors.New("broker unavailable")
	tests := map[string]offsetBroker{
		"request error": &fakeOffsetBroker{err: want},
		"missing topic": &fakeOffsetBroker{response: &sarama.OffsetCommitResponse{Errors: map[string]map[int32]sarama.KError{}}},
		"missing partition": &fakeOffsetBroker{response: &sarama.OffsetCommitResponse{Errors: map[string]map[int32]sarama.KError{
			"trigger-input": {},
		}}},
		"broker rejection": &fakeOffsetBroker{response: &sarama.OffsetCommitResponse{Errors: map[string]map[int32]sarama.KError{
			"trigger-input": {3: sarama.ErrNotCoordinatorForConsumer},
		}}},
	}
	for name, broker := range tests {
		name, broker := name, broker
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			committer := &SaramaOffsetCommitter{coordinator: fakeCoordinator{broker: broker}, groupID: "alarmd-shadow"}
			session := newFakeSession(context.Background(), &[]string{})
			err := committer.CommitOffset(context.Background(), session, consumer.Record{Topic: "trigger-input", Partition: 3, Offset: 41})
			if err == nil {
				t.Fatal("CommitOffset() accepted failed broker commit")
			}
			var retryable interface{ RetryableOffsetCommitDependency() }
			if !errors.As(err, &retryable) {
				t.Fatalf("CommitOffset() error = %T, want retryable offset dependency marker", err)
			}
		})
	}
}

func TestSaramaOffsetCommitterMarksCoordinatorFailureRetryable(t *testing.T) {
	t.Parallel()

	want := errors.New("coordinator unavailable")
	committer := &SaramaOffsetCommitter{coordinator: fakeCoordinator{err: want}, groupID: "alarmd-shadow"}
	err := committer.CommitOffset(
		context.Background(), newFakeSession(context.Background(), &[]string{}),
		consumer.Record{Topic: "trigger-input", Partition: 3, Offset: 41},
	)
	if !errors.Is(err, want) {
		t.Fatalf("CommitOffset() error = %v, want coordinator root cause", err)
	}
	var retryable interface{ RetryableOffsetCommitDependency() }
	if !errors.As(err, &retryable) {
		t.Fatalf("CommitOffset() error = %T, want retryable offset dependency marker", err)
	}
}

func TestSaramaOffsetCommitterLeavesLocalInvariantUnmarked(t *testing.T) {
	t.Parallel()

	committer := &SaramaOffsetCommitter{coordinator: fakeCoordinator{}, groupID: "alarmd-shadow"}
	err := committer.CommitOffset(
		context.Background(), newFakeSession(context.Background(), &[]string{}),
		consumer.Record{Topic: "trigger-input", Partition: 3, Offset: 41},
	)
	if err == nil {
		t.Fatal("CommitOffset() accepted a nil coordinator broker")
	}
	var retryable interface{ RetryableOffsetCommitDependency() }
	if errors.As(err, &retryable) {
		t.Fatalf("CommitOffset() local invariant = %T, must not be retryable", err)
	}
}

func TestSaramaOffsetCommitterPreservesLocalInvariantDuringCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	committer := &SaramaOffsetCommitter{
		coordinator: fakeCoordinator{broker: &fakeOffsetBroker{}}, groupID: "alarmd-shadow",
	}
	err := committer.CommitOffset(
		ctx, newFakeSession(ctx, &[]string{}),
		consumer.Record{Topic: "trigger-input", Partition: 3, Offset: -1},
	)
	if err == nil || errors.Is(err, context.Canceled) {
		t.Fatalf("CommitOffset() error = %v, want local offset invariant", err)
	}
	var retryable interface{ RetryableOffsetCommitDependency() }
	if errors.As(err, &retryable) {
		t.Fatalf("CommitOffset() local invariant = %T, must not be retryable", err)
	}
}

func TestSaramaOffsetCommitterPrefersSessionCancellationDuringCommit(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	broker := &fakeOffsetBroker{
		onCommit: func() {
			cancel()
		},
		response: &sarama.OffsetCommitResponse{Errors: map[string]map[int32]sarama.KError{
			"trigger-input": {3: sarama.ErrIllegalGeneration},
		}},
	}
	committer := &SaramaOffsetCommitter{coordinator: fakeCoordinator{broker: broker}, groupID: "alarmd-shadow"}
	session := newFakeSession(ctx, &[]string{})
	err := committer.CommitOffset(ctx, session, consumer.Record{Topic: "trigger-input", Partition: 3, Offset: 41})
	if !errors.Is(err, context.Canceled) || !errors.Is(err, sarama.ErrIllegalGeneration) {
		t.Fatalf("CommitOffset() error = %v, want cancellation and broker root cause", err)
	}
	var retryable interface{ RetryableOffsetCommitDependency() }
	if !errors.As(err, &retryable) {
		t.Fatalf("CommitOffset() error = %T, want retryable offset dependency marker", err)
	}
}

func TestSaramaOffsetCommitterPreservesSuccessfulAckDuringSessionCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	broker := &fakeOffsetBroker{onCommit: cancel}
	committer := &SaramaOffsetCommitter{coordinator: fakeCoordinator{broker: broker}, groupID: "alarmd-shadow"}
	session := newFakeSession(ctx, &[]string{})
	if err := committer.CommitOffset(ctx, session, consumer.Record{Topic: "trigger-input", Partition: 3, Offset: 41}); err != nil {
		t.Fatalf("CommitOffset() after successful ack = %v, want nil", err)
	}
}

type fakeCoordinator struct {
	broker offsetBroker
	err    error
}

func (c fakeCoordinator) Coordinator(string) (offsetBroker, error) {
	return c.broker, c.err
}

type fakeOffsetBroker struct {
	request  *sarama.OffsetCommitRequest
	response *sarama.OffsetCommitResponse
	err      error
	onCommit func()
}

func (b *fakeOffsetBroker) CommitOffset(request *sarama.OffsetCommitRequest) (*sarama.OffsetCommitResponse, error) {
	b.request = request
	if b.onCommit != nil {
		b.onCommit()
	}
	if b.err != nil {
		return nil, b.err
	}
	if b.response != nil {
		return b.response, nil
	}
	return &sarama.OffsetCommitResponse{Errors: map[string]map[int32]sarama.KError{
		"trigger-input": {3: sarama.ErrNoError},
	}}, nil
}
