// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package tasks

import (
	"context"
	"sync"

	"golang.org/x/sync/semaphore"
)

// Semaphore 统一信号量接口
type Semaphore interface {
	Acquire(ctx context.Context, n int64) error
	TryAcquire(n int64) bool
	Release(n int64)
}

type NoopSemaphore struct{}

func (ns *NoopSemaphore) Acquire(_ context.Context, _ int64) error {
	return nil
}

func (ns *NoopSemaphore) TryAcquire(_ int64) bool {
	return true
}

func (ns *NoopSemaphore) Release(_ int64) {
	return
}

// multiSemaphore 信号量组
type multiSemaphore struct {
	s1 *semaphore.Weighted
	s2 *semaphore.Weighted
}

// acquireLoop 循环获取信号量组成员
func acquireLoop[T any](
	// 信号量组
	m *multiSemaphore,
	// 信号量申请函数
	acquireFunc func(s *semaphore.Weighted, n int64) T,
	// 返回成功判断函数
	successFunc func(value T) bool,
	// 请求数值
	n int64,
	// 成功返回值
	successValue T,
) T {
	// 申请信号量s1
	r1 := acquireFunc(m.s1, n)
	if !successFunc(r1) {
		return r1
	}
	// 申请信号量s2
	r2 := acquireFunc(m.s2, n)
	if !successFunc(r2) {
		// 失败回退s1
		m.s1.Release(n)
		return r2
	}
	return successValue
}

// Acquire 尝试获取所有信号量
func (m *multiSemaphore) Acquire(ctx context.Context, n int64) error {
	return acquireLoop[error](m,
		func(s *semaphore.Weighted, n int64) error {
			return s.Acquire(ctx, n)
		},
		func(err error) bool {
			return err == nil
		},
		n,
		nil,
	)
}

// TryAcquire 非阻塞尝试获取所有信号量
func (m *multiSemaphore) TryAcquire(n int64) bool {
	return acquireLoop[bool](m,
		func(s *semaphore.Weighted, n int64) bool {
			return s.TryAcquire(n)
		},
		func(r bool) bool {
			return r
		},
		n,
		true,
	)
}

// Release 释放所有信号量
func (m *multiSemaphore) Release(n int64) {
	m.s1.Release(n)
	m.s2.Release(n)
}

type SemaphorePool struct {
	rw           sync.RWMutex
	semaphoreMap map[string]*semaphore.Weighted // 按key存放信号量对象*semaphore.Weighted
}

func (p *SemaphorePool) Delete(key string) {
	p.rw.Lock()
	delete(p.semaphoreMap, key)
	p.rw.Unlock()
}

// get 根据key和n获取缓存信号量对象，若不存在则新建并放入缓存
func (p *SemaphorePool) get(key string, n int64) *semaphore.Weighted {
	var s *semaphore.Weighted
	p.rw.Lock()
	if v, ok := p.semaphoreMap[key]; !ok {
		s = semaphore.NewWeighted(n)
		p.semaphoreMap[key] = s
	} else {
		s = v
	}
	p.rw.Unlock()
	return s
}

// GetSemaphore 获取信号量实例，weight以第一次配置为准，同pool所有实例同key共享信号量
func (p *SemaphorePool) GetSemaphore(key1 string, n1 int64, key2 string, n2 int64) Semaphore {
	return &multiSemaphore{
		s1: p.get(key1, n1),
		s2: p.get(key2, n2),
	}
}

// NewSemaphorePool 获取信号量池，同一池同key的信号量限制互通
func NewSemaphorePool() *SemaphorePool {
	return &SemaphorePool{
		semaphoreMap: make(map[string]*semaphore.Weighted),
	}
}

var DefaultSemaphorePool = NewSemaphorePool()
