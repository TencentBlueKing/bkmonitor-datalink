// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package consumer

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestRunClaimCommitsOnlyAfterProcessingSucceeds(t *testing.T) {
	t.Parallel()

	events := []string{}
	records := make(chan Record, 1)
	records <- Record{Key: []byte("key"), Value: []byte("value"), Offset: 7}
	close(records)
	processor := ProcessorFunc(func(context.Context, []byte, []byte) error {
		events = append(events, "process")
		return nil
	})
	committer := CommitterFunc(func(_ context.Context, record Record) error {
		events = append(events, "commit")
		if record.Offset != 7 {
			t.Fatalf("committed offset = %d, want record offset 7", record.Offset)
		}
		return nil
	})

	if err := RunClaim(context.Background(), records, func() Processor { return processor }, committer); err != nil {
		t.Fatalf("RunClaim() error = %v", err)
	}
	if !reflect.DeepEqual(events, []string{"process", "commit"}) {
		t.Fatalf("events = %v, want process then commit", events)
	}
}

func TestRunClaimStopsWithoutCommitOrReadingNextRecordOnFailure(t *testing.T) {
	t.Parallel()

	want := errors.New("processing failed")
	records := make(chan Record, 2)
	records <- Record{Value: []byte("first")}
	records <- Record{Value: []byte("second")}
	close(records)
	processed := 0
	commits := 0
	processor := ProcessorFunc(func(context.Context, []byte, []byte) error {
		processed++
		return want
	})
	committer := CommitterFunc(func(context.Context, Record) error {
		commits++
		return nil
	})

	if err := RunClaim(context.Background(), records, func() Processor { return processor }, committer); !errors.Is(err, want) {
		t.Fatalf("RunClaim() error = %v, want %v", err, want)
	}
	if processed != 1 || commits != 0 {
		t.Fatalf("processed=%d commits=%d, want 1/0", processed, commits)
	}
}

func TestRunClaimDoesNotCommitAfterCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	records := make(chan Record, 1)
	records <- Record{Value: []byte("value")}
	close(records)
	commits := 0
	processor := ProcessorFunc(func(context.Context, []byte, []byte) error {
		cancel()
		return nil
	})
	committer := CommitterFunc(func(context.Context, Record) error {
		commits++
		return nil
	})

	if err := RunClaim(ctx, records, func() Processor { return processor }, committer); !errors.Is(err, context.Canceled) {
		t.Fatalf("RunClaim() error = %v, want context canceled", err)
	}
	if commits != 0 {
		t.Fatalf("commits = %d, want 0", commits)
	}
}

func TestRunClaimStopsWhenCommitFails(t *testing.T) {
	t.Parallel()

	want := errors.New("commit failed")
	records := make(chan Record, 2)
	records <- Record{Value: []byte("first")}
	records <- Record{Value: []byte("second")}
	close(records)
	processed := 0
	processor := ProcessorFunc(func(context.Context, []byte, []byte) error {
		processed++
		return nil
	})
	committer := CommitterFunc(func(context.Context, Record) error { return want })

	if err := RunClaim(context.Background(), records, func() Processor { return processor }, committer); !errors.Is(err, want) {
		t.Fatalf("RunClaim() error = %v, want %v", err, want)
	}
	if processed != 1 {
		t.Fatalf("processed = %d, want 1", processed)
	}
}

func TestRunClaimCreatesOneProcessorPerClaim(t *testing.T) {
	t.Parallel()

	created := 0
	factory := func() Processor {
		created++
		return ProcessorFunc(func(context.Context, []byte, []byte) error { return nil })
	}
	committer := CommitterFunc(func(context.Context, Record) error { return nil })
	for claim := 0; claim < 2; claim++ {
		records := make(chan Record, 1)
		records <- Record{}
		close(records)
		if err := RunClaim(context.Background(), records, factory, committer); err != nil {
			t.Fatalf("RunClaim(%d) error = %v", claim, err)
		}
	}
	if created != 2 {
		t.Fatalf("created processors = %d, want one per claim", created)
	}
}

func TestRunClaimDoesNotCreateProcessorOrReadBufferedRecordWhenPreCanceled(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	records := make(chan Record, 1)
	records <- Record{}
	close(records)
	created := 0
	factory := func() Processor {
		created++
		return ProcessorFunc(func(context.Context, []byte, []byte) error { return nil })
	}

	if err := RunClaim(ctx, records, factory, CommitterFunc(func(context.Context, Record) error { return nil })); !errors.Is(err, context.Canceled) {
		t.Fatalf("RunClaim() error = %v, want context canceled", err)
	}
	if created != 0 || len(records) != 1 {
		t.Fatalf("created=%d buffered=%d, want 0/1", created, len(records))
	}
}
