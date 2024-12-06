// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package pool

import (
	"errors"
	"runtime"
	"sync"
	"time"

	ants "github.com/panjf2000/ants/v2"
)

var (
	maxRouting  = runtime.GOMAXPROCS(-1)
	defaultPool *ants.MultiPool
)

func check() error {
	if defaultPool == nil {
		return errors.New("pool is empty")
	}
	return nil
}

func Tune(size int) error {
	err := check()
	if err != nil {
		return err
	}
	defaultPool.Tune(size)
	return nil
}

func Submit(task func()) error {
	err := check()
	if err != nil {
		return err
	}
	return defaultPool.Submit(task)
}

func Running() int {
	err := check()
	if err != nil {
		return 0
	}
	return defaultPool.Running()
}

type Pool struct {
	lock sync.Mutex
	mp   *ants.MultiPool
}

func (p *Pool) Run() error {
	if p.mp == nil {
		mp, err := ants.NewMultiPool(maxRouting, -1, ants.LeastTasks)
		if err != nil {
			return err
		}
		p.lock.Lock()
		p.mp = mp
		p.lock.Unlock()
	}
	return nil
}

func (p *Pool) Close() error {
	err := p.mp.ReleaseTimeout(5 * time.Second)
	if err != nil {
		return err
	}
	return nil
}

func init() {
	defaultPool, _ = ants.NewMultiPool(maxRouting, -1, ants.LeastTasks)
}
