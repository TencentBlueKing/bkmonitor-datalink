// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package networkflow

import (
	"errors"
	"sync"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
)

var (
	ErrAlreadyStarted = errors.New("networkflow receiver already started")
	ErrNotStarted     = errors.New("networkflow receiver not started")
)

type RecordPublisher func(*define.Record)

type runtimeHandle interface {
	Start() error
	Stop() error
}

type config struct {
	Enabled   bool
	DataID    int32
	Listeners []string
}

type runtimeFactory func(config, RecordPublisher) (runtimeHandle, error)

type Receiver struct {
	mu      sync.Mutex
	config  config
	publish RecordPublisher
	factory runtimeFactory
	runtime runtimeHandle
	started bool
}

func New(enabled bool, dataID int32, listeners []string, publish RecordPublisher) *Receiver {
	return &Receiver{
		config:  newConfig(enabled, dataID, listeners),
		publish: publish,
		factory: newRuntime,
	}
}

func newConfig(enabled bool, dataID int32, listeners []string) config {
	return config{
		Enabled:   enabled,
		DataID:    dataID,
		Listeners: append([]string(nil), listeners...),
	}
}

func (r *Receiver) Start() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.started {
		return ErrAlreadyStarted
	}
	r.started = true
	if !r.config.Enabled {
		return nil
	}

	rt, err := r.factory(r.config, r.publish)
	if err != nil {
		r.started = false
		return err
	}
	if err := rt.Start(); err != nil {
		r.started = false
		return err
	}
	r.runtime = rt
	return nil
}

func (r *Receiver) Stop() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.started {
		return ErrNotStarted
	}
	if r.runtime != nil {
		if err := r.runtime.Stop(); err != nil {
			return err
		}
	}
	r.runtime = nil
	r.started = false
	return nil
}
