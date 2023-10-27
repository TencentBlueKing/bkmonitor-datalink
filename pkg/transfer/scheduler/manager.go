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

	"github.com/emirpasic/gods/maps/treemap"
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/logging"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

// PipelineManager
type PipelineManager struct {
	define.Atomic
	terminateTimeout time.Duration
	pipelines        *treemap.Map
}

func (p *PipelineManager) getItem(dataID int) *PipelineItem {
	v, ok := p.pipelines.Get(dataID)
	if ok && v != nil {
		return v.(*PipelineItem)
	}
	return nil
}

// GetItem :
func (p *PipelineManager) GetItem(dataID int) (item *PipelineItem, err error) {
	p.View(func() {
		item = p.getItem(dataID)
		if item == nil {
			err = errors.Wrapf(define.ErrItemNotFound, "pipeline item %v not found", dataID)
		}
	})
	return
}

// GetPipeline :
func (p *PipelineManager) GetPipeline(dataID int) define.Pipeline {
	item, err := p.GetItem(dataID)
	if err != nil {
		return nil
	}
	return item.Pipeline
}

func (p *PipelineManager) eachItem(fn func(int, *PipelineItem) error) error {
	iterator := p.pipelines.Iterator()
	for iterator.Next() {
		dataID := iterator.Key().(int)
		item := iterator.Value().(*PipelineItem)
		err := fn(dataID, item)
		if err != nil {
			return err
		}
	}
	return nil
}

// EachItem :
func (p *PipelineManager) EachItem(fn func(int, *PipelineItem) error) error {
	return p.ViewE(func() error {
		return p.eachItem(fn)
	})
}

// IsAlive :
func (p *PipelineManager) IsAlive(dataID int) (ok bool) {
	var item *PipelineItem
	p.View(func() {
		item = p.getItem(dataID)
	})
	if item != nil {
		item.View(func() {
			ok = item.IsAlive()
		})
	}
	return
}

// Count
func (p *PipelineManager) Count() (count int) {
	p.View(func() {
		count = p.pipelines.Size()
	})
	return
}

// Activate : add pipeline to map and start it
func (p *PipelineManager) Activate(ctx context.Context, conf *config.PipelineConfig) (err error) {
	defer utils.RecoverError(func(e error) {
		err = e
		logging.Errorf("activate pipeline %v panic %+v", conf.DataID, e)
		MonitorPipelinePanic.Inc()
	})

	logging.Infof("activating pipeline %v", conf.DataID)
	err = p.activate(ctx, conf)
	if err == nil {
		MonitorRunningPipeline.Inc()
	}

	MonitorDeclaredPipeline.Set(float64(p.Count()))
	return
}

func (p *PipelineManager) activate(ctx context.Context, conf *config.PipelineConfig) (err error) {
	subCtx := config.PipelineConfigIntoContext(ctx, conf)
	subCtx = config.MQConfigIntoContext(subCtx, conf.MQConfig)
	pipeCtx, cancel := context.WithCancel(subCtx)
	pipeline, err := define.NewPipeline(pipeCtx, conf.ETLConfig)
	if err != nil {
		cancel()
		return errors.Wrapf(err, "create pipeline %v failed", conf.DataID)
	}

	item := NewPipelineItem(subCtx, cancel, conf, pipeline)
	err = p.UpdateE(func() error {
		_, ok := p.pipelines.Get(conf.DataID)
		if ok {
			return errors.Wrapf(define.ErrItemAlreadyExists, "pipeline %v already exists", conf.DataID)
		}

		p.pipelines.Put(conf.DataID, item)
		return nil
	})
	if err != nil {
		return err
	}

	return item.UpdateE(item.Start)
}

// Deactivate : remove pipeline from map and kill it
func (p *PipelineManager) Deactivate(dataID int) (err error) {
	defer utils.RecoverError(func(e error) {
		err = e
		logging.Errorf("deactivate pipeline %v panic %+v", dataID, e)
		MonitorPipelinePanic.Inc()
	})

	logging.Infof("deactivating pipeline %v", dataID)
	err = p.deactivate(dataID)
	if err != nil {
		MonitorRunningPipeline.Dec()
	}
	MonitorDeclaredPipeline.Set(float64(p.Count()))

	return
}

func (p *PipelineManager) deactivate(dataID int) (err error) {
	var item *PipelineItem
	p.View(func() {
		item = p.getItem(dataID)
	})
	if item == nil {
		return errors.Wrapf(define.ErrItemNotFound, "pipeline item %v not found", dataID)
	}

	p.Update(func() {
		p.pipelines.Remove(dataID)
	})

	return item.UpdateE(func() error {
		return item.Terminate(p.terminateTimeout)
	})
}

// EachAliveItem
func (p *PipelineManager) EachAliveItem(fn func(int, *PipelineItem) error) error {
	return p.EachItem(func(i int, item *PipelineItem) error {
		if item.IsAlive() {
			return fn(i, item)
		}
		return nil
	})
}

// IsConfigChanged :
func (p *PipelineManager) IsConfigChanged(pipe *config.PipelineConfig) bool {
	var item *PipelineItem
	p.View(func() {
		item = p.getItem(pipe.DataID)
	})
	if item == nil {
		return true
	}

	return utils.HashIt(item.Config) != utils.HashIt(pipe)
}

// Reactivate :
func (p *PipelineManager) Reactivate(ctx context.Context, conf *config.PipelineConfig) (err error) {
	defer utils.RecoverError(func(e error) {
		err = e
		logging.Errorf("reactivate pipeline %v panic %+v", conf.DataID, e)
		MonitorPipelinePanic.Inc()
	})

	logging.Infof("reactivating pipeline %v", conf.DataID)
	err = p.reactivate(ctx, conf)

	MonitorDeclaredPipeline.Set(float64(p.Count()))

	return
}

func (p *PipelineManager) reactivate(ctx context.Context, pipe *config.PipelineConfig) (err error) {
	err = p.deactivate(pipe.DataID)
	if err != nil {
		logging.Errorf("deactivate pipeline %d failed: %v", pipe.DataID, err)
	}
	err = p.activate(ctx, pipe)
	if err != nil {
		logging.Errorf("activate pipeline %d failed: %v", pipe.DataID, err)
	}
	return
}

// NewPipelineManager
func NewPipelineManager(terminateTimeout time.Duration) *PipelineManager {
	return &PipelineManager{
		terminateTimeout: terminateTimeout,
		pipelines:        treemap.NewWithIntComparator(),
	}
}
