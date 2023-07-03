// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package scheduler

import (
	"context"
	"time"

	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/logging"
)

// Watcher :
type Watcher func(ctx context.Context) <-chan *define.WatchEvent

// PipelineItemReady :
const (
	// PipelineItemReady :
	PipelineItemReady = "ready"
	// PipelineItemRunning :
	PipelineItemRunning = "running"
	// PipelineItemClosing :
	PipelineItemClosing = "closing"
	// PipelineItemClosed :
	PipelineItemClosed = "closed"
	// PipelineItemError :
	PipelineItemError = "error"
)

// PipelineItem :
type PipelineItem struct {
	define.Atomic
	ctx      context.Context
	cancel   context.CancelFunc
	Status   string
	Pipeline define.Pipeline
	Config   *config.PipelineConfig
	KillChan <-chan error
}

// IsAlive :
func (i *PipelineItem) IsAlive() bool {
	switch i.Status {
	case PipelineItemReady, PipelineItemClosed:
		return false
	default:
		return true
	}
}

// Start :
func (i *PipelineItem) Start() (err error) {
	switch i.Status {
	case PipelineItemReady:
		killCh := i.Pipeline.Start()
		i.KillChan = killCh
		if killCh == nil {
			err = i.errorf("pipeline %v start return nil kill channel", i.Pipeline)
		}
		i.Status = PipelineItemRunning
	case PipelineItemRunning:
		break
	default:
		err = i.errorf("pipeline %v can not start in status %v", i.Pipeline, i.Status)
	}
	return
}

func (i *PipelineItem) errorf(message string, v ...interface{}) error {
	i.Status = PipelineItemError
	return errors.Wrapf(define.ErrOperationForbidden, message, v...)
}

// Terminate : stop and wait pipeline
func (i *PipelineItem) Terminate(timeout time.Duration) error {
	timeout = timeout / 2
	err := i.Stop(timeout)
	if err != nil {
		return err
	}
	return i.Wait(timeout)
}

// Stop :
func (i *PipelineItem) Stop(timeout time.Duration) (err error) {
	var ok bool
	switch i.Status {
	case PipelineItemRunning:
		pipeline := i.Pipeline
		killCh := i.KillChan
		go func() {
		loop:
			for {
				select {
				case <-i.ctx.Done():
					break loop
				case err, ok = <-killCh:
					if !ok {
						break loop
					}
					logging.Warnf("received error %v when pipeline %v closing", err, pipeline)
				}
			}
		}()

		err = pipeline.Stop(timeout)
		if err == nil {
			i.Status = PipelineItemClosing
			return
		}
	case PipelineItemClosing:
		break
	default:
		err = i.errorf("pipeline %v can not stop in status %v", i.Pipeline, i.Status)
	}
	return
}

// Wait :
func (i *PipelineItem) Wait(timeout time.Duration) (err error) {
	switch i.Status {
	case PipelineItemClosing:
		go func() {
			time.Sleep(timeout)
			i.cancel()
		}()

		err = i.Pipeline.Wait()
		if err == nil {
			i.Status = PipelineItemClosed
			return
		}
	case PipelineItemClosed:
		break
	default:
		err = i.errorf("pipeline %v can not wait in status %v", i.Pipeline, i.Status)
	}
	return
}

// NewPipelineItem :
func NewPipelineItem(ctx context.Context, cancel context.CancelFunc, config *config.PipelineConfig, pipeline define.Pipeline) *PipelineItem {
	return &PipelineItem{
		ctx:      ctx,
		cancel:   cancel,
		Pipeline: pipeline,
		Config:   config,
		Status:   PipelineItemReady,
	}
}

// FromContext : get scheduler from context
func FromContext(ctx context.Context) *Scheduler {
	conf := ctx.Value(define.ContextSchedulerKey)
	if conf == nil {
		return nil
	}
	return conf.(*Scheduler)
}

// IntoContext : put scheduler into context
func IntoContext(ctx context.Context, scheduler *Scheduler) context.Context {
	return context.WithValue(ctx, define.ContextSchedulerKey, scheduler)
}
