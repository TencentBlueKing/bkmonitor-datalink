// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package merge

import (
	"slices"
	"sync"
	"time"
)

type memberStage struct{ tenant, relation, phase string }

func stageOf(b MemberBatch) memberStage {
	return memberStage{b.Facts.TenantID, b.Facts.ID, b.Phase}
}

type memberTask struct {
	batch   MemberBatch
	running bool
	retryAt time.Time
}

// MemberPool 保存单进程的短时批次和完成结果；丢失后由固定关系重建，不依赖中间存储。
type MemberPool struct {
	mu     sync.Mutex
	stages map[memberStage][]*memberTask
	wake   chan struct{}
}

// NewMemberPool 创建内存批次池，不启动任何 Worker goroutine。
func NewMemberPool() *MemberPool {
	return &MemberPool{stages: map[memberStage][]*memberTask{}, wake: make(chan struct{}, 1)}
}

// Wake 返回新增工作或执行名额释放时的唤醒信号。
func (p *MemberPool) Wake() <-chan struct{} { return p.wake }

// Signal 唤醒调度器；连续通知可以合并，工作本身保存在内存集合中。
func (p *MemberPool) Signal() {
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

// Stage 登记固定阶段，或者返回该阶段当前的逐批结果；不会将重复登记重置为待处理。
func (p *MemberPool) Stage(wanted []MemberBatch) ([]MemberBatch, bool) {
	if len(wanted) == 0 {
		return nil, true
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	key := stageOf(wanted[0])
	items, exists := p.stages[key]
	if !exists {
		for _, b := range wanted {
			b.Members = slices.Clone(b.Members)
			items = append(items, &memberTask{batch: b})
		}
		p.stages[key] = items
		p.Signal()
	}
	out, done := make([]MemberBatch, len(items)), true
	for i, item := range items {
		out[i] = item.batch
		out[i].Members = slices.Clone(item.batch.Members)
		done = done && item.batch.State == "completed"
	}
	return out, done
}

// Take 只取空闲名额可容纳的批次，调用方随后才启动执行 goroutine。
func (p *MemberPool) Take(limit int, at time.Time) []MemberBatch {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := []MemberBatch{}
	for _, items := range p.stages {
		for _, item := range items {
			if len(out) >= limit {
				return out
			}
			if item.running || item.batch.State == "completed" || at.Before(item.retryAt) {
				continue
			}
			item.running = true
			b := item.batch
			b.Members = slices.Clone(b.Members)
			out = append(out, b)
		}
	}
	return out
}

// Finish 合并本次结果；失败的批次短暂退避，已成功成员留在内存中，重试不会重复执行。
// 返回 true 表示本次补齐整个阶段，调用方直接推进原组级执行器。
func (p *MemberPool) Finish(b MemberBatch, err error, at time.Time) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	items := p.stages[stageOf(b)]
	found := false
	for _, item := range items {
		if item.batch.Key != b.Key || !item.running {
			continue
		}
		item.running = false
		b.Members = slices.Clone(b.Members)
		item.batch = b
		if err != nil {
			item.retryAt = at.Add(time.Second)
		}
		found = true
		break
	}
	if !found {
		return false
	}
	for _, item := range items {
		if item.batch.State != "completed" {
			return false
		}
	}
	return true
}

func (p *MemberPool) forget(tenant, relation, phase string) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.stages, memberStage{tenant, relation, phase})
}
