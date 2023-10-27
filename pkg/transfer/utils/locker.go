// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package utils

import (
	"context"

	"golang.org/x/sync/semaphore"
)

// Semaphore :
type Semaphore interface {
	Acquire(ctx context.Context, n int64) error
	TryAcquire(n int64) bool
	Release(n int64)
}

// NewWeightedSemaphore
var NewWeightedSemaphore = semaphore.NewWeighted

// ChainingSemaphore
type ChainingSemaphore struct {
	child  Semaphore
	parent Semaphore
}

// Acquire
func (s *ChainingSemaphore) Acquire(ctx context.Context, n int64) error {
	err := s.child.Acquire(ctx, n)
	if err != nil {
		return err
	}

	return s.parent.Acquire(ctx, n)
}

// TryAcquire
func (s *ChainingSemaphore) TryAcquire(n int64) bool {
	if !s.child.TryAcquire(n) {
		return false
	}
	if !s.parent.TryAcquire(n) {
		s.child.Release(n)
		return false
	}
	return true
}

// Release
func (s *ChainingSemaphore) Release(n int64) {
	s.parent.Release(n)
	s.child.Release(n)
}

// NewChainingSemaphore
func NewChainingSemaphore(parent, child Semaphore) *ChainingSemaphore {
	return &ChainingSemaphore{
		child:  child,
		parent: parent,
	}
}
