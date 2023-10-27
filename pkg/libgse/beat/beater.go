// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package beat

import (
	"fmt"
	"sync"

	"github.com/elastic/beats/libbeat/beat"
	"github.com/elastic/beats/libbeat/common"
)

// BeaterState is beater's state
type BeaterState int

const (
	BeaterBeforeOpening BeaterState = iota
	BeaterFailToOpen
	BeaterRunning
	BeaterStoped
)

// BKBeat
type BKBeat struct {
	Beat        *beat.Beat
	Client      *beat.Client
	LocalConfig *Config
	BeaterState BeaterState
	Finished    bool
	Done        chan struct{}
}

var commonBKBeat = BKBeat{
	Finished:    false,
	BeaterState: BeaterBeforeOpening,
	Done:        make(chan struct{}),
}

// 默认使用非堵塞发送
var (
	publishConfig    PublishConfig
	wg               sync.WaitGroup
	errorMessageChan chan error
)

func creator(b *beat.Beat, localConfig *common.Config) (beat.Beater, error) {
	commonBKBeat.Beat = b
	commonBKBeat.LocalConfig = localConfig
	if nil == b || nil == localConfig {
		return nil, fmt.Errorf("%s failed to initialize", b.Info.Beat)
	}
	return &commonBKBeat, nil
}

// Run
func (bkb *BKBeat) Run(b *beat.Beat) error {
	bkb.Beat = b
	var err error
	if publishConfig.ACKEvents != nil {
		err = commonBKBeat.Beat.Publisher.SetACKHandler(beat.PipelineACKHandler{
			ACKEvents: publishConfig.ACKEvents,
		})
		if err != nil {
			bkb.Client = nil
			return err
		}
	}
	client, err := commonBKBeat.Beat.Publisher.ConnectWith(publishConfig)
	if nil != err {
		bkb.Client = nil
		return err
	}
	bkb.Client = &client
	wg.Done()
	close(errorMessageChan)
	select {
	case <-bkb.Done:
		bkb.Finished = true
		return nil
	}
}

// Stop
func (bkb *BKBeat) Stop() {
	if BeaterRunning != commonBKBeat.BeaterState {
		return
	}
	if nil == bkb.Client {
		return
	}
	(*bkb.Client).Close()
	bkb.Client = nil
	close(bkb.Done)
	freeResource()
}

// Reload
func (bkb *BKBeat) Reload(localConfig *common.Config) {
	commonBKBeat.LocalConfig = localConfig
	ReloadChan <- true
}

func beatNotRunning() bool {
	return commonBKBeat.BeaterState != BeaterRunning
}
