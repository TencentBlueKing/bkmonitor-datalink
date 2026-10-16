// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License.

package v1beta3

import (
	"context"
	"fmt"
	"sync"

	pl "github.com/prometheus/prometheus/promql"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
)

type topologyReadyEvent struct {
	matrix   *topologyFetchEvent
	children []topologyFetchGroup
	finished bool
	err      error
}

// Cardinality branches are independent, so they join the same four-worker
// queue as relation roots. Only query plans wait in the queue: each worker
// retains at most one Matrix and waits for the single graph writer's ack.
// Time shards stay within a worker to reuse their adaptive successful width.
func (loader *timeGraphMatrixLoader) fetchTopologyReadyGroups(groups []topologyFetchGroup, space string, queries *int) error {
	if meter := loader.matrixMemory(); meter != nil {
		if err := meter.release("current"); err != nil {
			return err
		}
	}
	pending := make([]topologyFetchGroup, len(groups))
	cancels := make([]context.CancelFunc, len(groups))
	for i, group := range groups {
		group.ctx, cancels[i] = context.WithCancel(group.ctx)
		pending[i] = group
	}
	const workers = 4
	jobs := make(chan topologyFetchGroup)
	events := make(chan topologyReadyEvent)
	stop := make(chan struct{})
	var running sync.WaitGroup
	totals := make([]timeGraphMatrixLoader, workers)
	counts := make([]int, workers)
	defer func() {
		for _, cancel := range cancels {
			cancel()
		}
		close(stop)
		running.Wait()
		for i := range totals {
			child := &totals[i]
			*queries += counts[i]
			loader.queryCount += child.queryCount
			loader.pointCount += child.pointCount
			loader.queryDuration += child.queryDuration
			loader.physicalQueryCount += child.physicalQueryCount
			loader.reusedQueryCount += child.reusedQueryCount
		}
	}()
	send := func(event topologyReadyEvent) bool {
		select {
		case events <- event:
			return true
		case <-stop:
			return false
		}
	}
	for worker := 0; worker < workers; worker++ {
		running.Add(1)
		go func(worker int) {
			defer running.Done()
			for {
				var group topologyFetchGroup
				select {
				case group = <-jobs:
				case <-stop:
					return
				}
				child := *loader
				child.queryCount, child.pointCount, child.physicalQueryCount, child.reusedQueryCount = 0, 0, 0, 0
				child.queryDuration = 0
				child.lastMatrix = timeGraphMatrixSlot{}
				child.matrixToken = fmt.Sprintf("parallel-%d", worker)
				child.partitionDispatch = func(ctx context.Context, parts []*structured.QueryTs, fields []string, depth int) error {
					children := make([]topologyFetchGroup, len(parts))
					for i, part := range parts {
						children[i] = group
						children[i].ctx, children[i].query = ctx, part
						children[i].shardDepth = depth
						children[i].shardPlan = &timeGraphShardPlan{partitionFields: fields}
					}
					if !send(topologyReadyEvent{children: children}) {
						return context.Canceled
					}
					return nil
				}
				deliver := func(ctx context.Context, matrix pl.Matrix, apply func(context.Context, pl.Matrix) error) error {
					ack := make(chan error, 1)
					event := &topologyFetchEvent{ctx: ctx, matrix: matrix, apply: apply, ack: ack}
					if !send(topologyReadyEvent{matrix: event}) {
						return context.Canceled
					}
					select {
					case err := <-ack:
						return err
					case <-group.ctx.Done():
						return group.ctx.Err()
					}
				}
				count := 0
				err := child.fetchTopologyGroupWithDelivery(group, space, &count, nil, deliver)
				child.lastMatrix = timeGraphMatrixSlot{}
				if meter := child.matrixMemory(); meter != nil {
					if releaseErr := meter.release(child.matrixToken); err == nil {
						err = releaseErr
					}
				}
				if group.shardPlan == nil {
					counts[worker] += count
				}
				total := &totals[worker]
				total.queryCount += child.queryCount
				total.pointCount += child.pointCount
				total.queryDuration += child.queryDuration
				total.physicalQueryCount += child.physicalQueryCount
				total.reusedQueryCount += child.reusedQueryCount
				if !send(topologyReadyEvent{finished: true, err: err}) {
					return
				}
			}
		}(worker)
	}
	remaining := len(pending)
	for remaining > 0 {
		var dispatch chan topologyFetchGroup
		var next topologyFetchGroup
		if len(pending) > 0 {
			dispatch, next = jobs, pending[0]
		}
		select {
		case dispatch <- next:
			pending[0] = topologyFetchGroup{}
			pending = pending[1:]
		case event := <-events:
			if event.err != nil {
				return event.err
			}
			if event.matrix != nil {
				matrix := event.matrix
				err := matrix.apply(matrix.ctx, matrix.matrix)
				matrix.ack <- err
				if err != nil {
					return err
				}
			}
			pending = append(pending, event.children...)
			remaining += len(event.children)
			if event.finished {
				remaining--
			}
		case <-groups[0].ctx.Done():
			return groups[0].ctx.Err()
		}
	}
	return nil
}
