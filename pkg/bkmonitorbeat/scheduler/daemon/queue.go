// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package daemon

import (
	"sync"
	"time"

	"github.com/emirpasic/gods/lists/doublylinkedlist"
)

// JobQueue :
type JobQueue = *LockQueue

// LockQueue :
type LockQueue struct {
	lock sync.RWMutex
	jobs *doublylinkedlist.List
}

// Push :
func (q *LockQueue) Push(js ...Job) {
	q.lock.Lock()
	for _, j := range js {
		q.jobs.Add(j)
	}
	q.jobs.Sort(JobTimeComparator)
	q.lock.Unlock()
}

// First :
func (q *LockQueue) First() Job {
	q.lock.RLock()
	el, ok := q.jobs.Get(0)
	q.lock.RUnlock()
	if !ok {
		return nil
	}
	return el.(Job)
}

// Clear :
func (q *LockQueue) Clear() {
	q.lock.Lock()
	q.jobs.Clear()
	q.lock.Unlock()
}

// Pop :
func (q *LockQueue) Pop() Job {
	q.lock.Lock()
	el, ok := q.jobs.Get(0)
	if ok {
		q.jobs.Remove(0)
	}
	q.lock.Unlock()
	if !ok {
		return nil
	}
	return el.(Job)
}

// PopAll :
func (q *LockQueue) PopAll() []Job {
	q.lock.Lock()
	jobs := make([]Job, 0)
	iter := q.jobs.Iterator()

	for iter.Next() {
		j := iter.Value().(Job)
		jobs = append(jobs, j)
	}
	q.jobs.Clear()
	q.lock.Unlock()
	return jobs
}

// PopUntil :
func (q *LockQueue) PopUntil(now time.Time) []Job {
	q.lock.Lock()
	jobs := make([]Job, 0)
	iter := q.jobs.Iterator()

	for iter.Next() {
		j := iter.Value().(Job)
		if j.GetCheckTime().After(now) {
			break
		}
		jobs = append(jobs, j)
	}

	for i := 0; i < len(jobs); i++ {
		q.jobs.Remove(0)
	}

	q.lock.Unlock()

	return jobs
}

// Size :
func (q *LockQueue) Size() int {
	q.lock.RLock()
	defer q.lock.RUnlock()
	return q.Size()
}

// NewLockQueue :
func NewLockQueue() JobQueue {
	queue := &LockQueue{
		jobs: doublylinkedlist.New(),
	}
	return queue
}
