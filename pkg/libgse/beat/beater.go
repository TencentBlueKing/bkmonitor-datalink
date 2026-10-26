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

	// clientMu protects Client. Send/SendEvent may run concurrently with the
	// framework shutdown path; reading and clearing the pointer without a lock
	// races with BKBeat.Stop and can dereference nil.
	clientMu sync.RWMutex

	// finishOnce makes the teardown idempotent: the framework shutdown path
	// and Stop may both try to finish the same beater.
	finishOnce sync.Once
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
			bkb.setClient(nil)
			return err
		}
	}
	// The ACK callback is installed globally above. Passing the same callback
	// through the client config would invoke it a second time for every batch:
	// the pipeline calls the pipeline-level callback before the client-level
	// one. Keep a single callback so state updates and preview accounting are
	// not applied twice. WaitClose does not depend on this field.
	clientConfig := publishConfig
	clientConfig.ACKEvents = nil
	client, err := commonBKBeat.Beat.Publisher.ConnectWith(clientConfig)
	if nil != err {
		bkb.setClient(nil)
		return err
	}
	bkb.setClient(&client)
	wg.Done()
	close(errorMessageChan)
	select {
	case <-bkb.Done:
		bkb.Finished = true
		return nil
	}
}

func (bkb *BKBeat) getClient() *beat.Client {
	bkb.clientMu.RLock()
	defer bkb.clientMu.RUnlock()
	return bkb.Client
}

func (bkb *BKBeat) setClient(client *beat.Client) {
	bkb.clientMu.Lock()
	bkb.Client = client
	bkb.clientMu.Unlock()
}

// stopPublish releases the publisher client exactly once. It is separate from
// Stop so the managed preview can keep the client alive while it drains and
// close it only after Registrar.Stop.
func (bkb *BKBeat) stopPublish() {
	bkb.clientMu.Lock()
	client := bkb.Client
	bkb.Client = nil
	bkb.clientMu.Unlock()

	if client != nil {
		(*client).Close()
	}
}

// Stop
func (bkb *BKBeat) Stop() {
	if BeaterRunning != commonBKBeat.BeaterState {
		return
	}
	bkb.finishOnce.Do(func() {
		if managedPreview.isEnabled() {
			// Managed preview: signal the beater loop only. The coordinator
			// stops Kafka production, drains the pipeline, then closes the
			// client and releases storage after Registrar.Stop.
			if bkb.Done != nil {
				close(bkb.Done)
			}
			return
		}

		bkb.stopPublish()
		if bkb.Done != nil {
			close(bkb.Done)
		}
		freeResource()
	})
}

// Reload
func (bkb *BKBeat) Reload(localConfig *common.Config) {
	commonBKBeat.LocalConfig = localConfig
	ReloadChan <- true
}

func beatNotRunning() bool {
	return commonBKBeat.BeaterState != BeaterRunning
}
