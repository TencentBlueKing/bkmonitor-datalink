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
)

type Record struct {
	Key       []byte
	Value     []byte
	Topic     string
	Partition int32
	Offset    int64
}

type Processor interface {
	Process(context.Context, []byte, []byte) error
}

type ProcessorFunc func(context.Context, []byte, []byte) error

func (f ProcessorFunc) Process(ctx context.Context, key, value []byte) error {
	return f(ctx, key, value)
}

type ProcessorFactory func() Processor

type Committer interface {
	// CommitProcessed records that the complete record has been processed. A
	// Kafka adapter must commit record.Offset+1 and return only after the broker
	// acknowledges it, or return the commit error.
	CommitProcessed(context.Context, Record) error
}

type CommitterFunc func(context.Context, Record) error

func (f CommitterFunc) CommitProcessed(ctx context.Context, record Record) error {
	return f(ctx, record)
}

// Claim owns exactly one Processor for the lifetime of one transport claim.
type Claim struct {
	processor Processor
	committer Committer
}

func NewClaim(newProcessor ProcessorFactory, committer Committer) (*Claim, error) {
	if newProcessor == nil || committer == nil {
		return nil, errors.New("consumer claim: processor factory and committer are required")
	}
	processor := newProcessor()
	if processor == nil {
		return nil, errors.New("consumer claim: processor factory returned nil")
	}
	return &Claim{processor: processor, committer: committer}, nil
}

func (c *Claim) Process(ctx context.Context, record Record) error {
	if c == nil || c.processor == nil || c.committer == nil {
		return errors.New("consumer claim: initialized claim is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := c.processor.Process(ctx, record.Key, record.Value); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return c.committer.CommitProcessed(ctx, record)
}

// RunClaim creates and owns one claim-local Processor, then processes one
// record at a time so rebalance cannot detach window state from ownership.
func RunClaim(ctx context.Context, records <-chan Record, newProcessor ProcessorFactory, committer Committer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	claim, err := NewClaim(newProcessor, committer)
	if err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case record, ok := <-records:
			if !ok {
				return nil
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := claim.Process(ctx, record); err != nil {
				return err
			}
		}
	}
}
