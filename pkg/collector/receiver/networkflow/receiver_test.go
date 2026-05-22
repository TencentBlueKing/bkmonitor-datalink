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
	"net/netip"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	flowutils "github.com/netsampler/goflow2/v2/utils"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
)

func TestFlowTemplateNotReady(t *testing.T) {
	published := make([]*define.Record, 0)
	producer, err := newFlowRecordProducer(320001, func(record *define.Record) {
		published = append(published, record)
	})
	require.NoError(t, err)
	defer producer.Close()

	pipe := flowutils.NewNetFlowPipe(&flowutils.PipeConfig{Producer: producer})
	err = pipe.DecodeFlow(&flowutils.Message{
		Src:      netip.MustParseAddrPort("192.0.2.10:9995"),
		Dst:      netip.MustParseAddrPort("192.0.2.1:2055"),
		Received: time.Unix(1710000007, 0),
		Payload: []byte{
			0x00, 0x09,
			0x00, 0x01,
			0x00, 0x00, 0x00, 0x00,
			0x00, 0x00, 0x00, 0x01,
			0x00, 0x00, 0x00, 0x01,
			0x00, 0x00, 0x00, 0x2a,
			0x01, 0x00,
			0x00, 0x08,
			0x00, 0x00, 0x00, 0x00,
		},
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "template")
	assert.Empty(t, published)
}

func TestFlowProducerSupportsNetFlowV9SamplingState(t *testing.T) {
	published := make([]*define.Record, 0)
	producer, err := newFlowRecordProducer(320001, func(record *define.Record) {
		published = append(published, record)
	})
	require.NoError(t, err)
	defer producer.Close()

	pipe := flowutils.NewNetFlowPipe(&flowutils.PipeConfig{Producer: producer})
	templateAndDataPayload := []byte{
		0x00, 0x09,
		0x00, 0x02,
		0x00, 0x00, 0x00, 0x64,
		0x66, 0x38, 0x1f, 0x6b,
		0x00, 0x00, 0x00, 0x01,
		0x00, 0x00, 0x00, 0x2a,
		0x00, 0x00,
		0x00, 0x14,
		0x04, 0x00,
		0x00, 0x03,
		0x00, 0x08, 0x00, 0x04,
		0x00, 0x0c, 0x00, 0x04,
		0x00, 0x01, 0x00, 0x04,
		0x04, 0x00,
		0x00, 0x10,
		0xc0, 0x00, 0x02, 0x0a,
		0xc6, 0x33, 0x64, 0x0a,
		0x00, 0x00, 0x00, 0x2a,
	}

	err = pipe.DecodeFlow(&flowutils.Message{
		Src:      netip.MustParseAddrPort("192.0.2.10:9995"),
		Dst:      netip.MustParseAddrPort("192.0.2.1:2055"),
		Received: time.Unix(1710000008, 0),
		Payload:  templateAndDataPayload,
	})

	require.NoError(t, err)
	require.Len(t, published, 1)
	data, ok := published[0].Data.(*Data)
	require.True(t, ok)
	assert.Equal(t, int32(320001), data.DataID)
	assert.Equal(t, uint64(42), data.Bytes)
}

func TestNetworkflowLifecycle(t *testing.T) {
	events := make([]string, 0)
	runtime1 := &fakeRuntime{name: "runtime-1", events: &events}
	runtime2 := &fakeRuntime{name: "runtime-2", events: &events}
	runtime3 := &fakeRuntime{name: "runtime-3", events: &events, startErr: errors.New("boom")}
	runtimes := []*fakeRuntime{runtime1, runtime2, runtime3}

	receiver := New(true, 320001, []string{"netflow://127.0.0.1:2055"}, nil)
	receiver.factory = func(cfg config, publish RecordPublisher) (runtimeHandle, error) {
		runtime := runtimes[0]
		runtimes = runtimes[1:]
		return runtime, nil
	}

	require.NoError(t, receiver.Start())
	assert.Same(t, runtime1, receiver.runtime)
	assert.Equal(t, []string{"start:runtime-1"}, events)

	err := receiver.Start()
	require.ErrorIs(t, err, ErrAlreadyStarted)
	assert.Equal(t, []string{"start:runtime-1"}, events)

	require.NoError(t, receiver.Stop())
	assert.Equal(t, []string{"start:runtime-1", "stop:runtime-1"}, events)

	err = receiver.Stop()
	require.ErrorIs(t, err, ErrNotStarted)
}

type fakeRuntime struct {
	name     string
	startErr error
	started  bool
	stopped  bool
	events   *[]string
}

func (r *fakeRuntime) Start() error {
	*r.events = append(*r.events, "start:"+r.name)
	if r.startErr != nil {
		return r.startErr
	}
	r.started = true
	return nil
}

func (r *fakeRuntime) Stop() error {
	*r.events = append(*r.events, "stop:"+r.name)
	r.stopped = true
	return nil
}

func TestLoadNetworkflowConfig(t *testing.T) {
	specs, err := parseListeners([]string{
		"netflow://0.0.0.0:2055",
		"ipfix://0.0.0.0:4739",
		"sflow://0.0.0.0:6343",
		"flow://0.0.0.0:2055",
	})
	require.NoError(t, err)
	require.Len(t, specs, 4)
	assert.Equal(t, "netflow", specs[0].Scheme)
	assert.Equal(t, 2055, specs[0].Port)
	assert.Equal(t, "0.0.0.0", specs[0].Hostname)
	assert.Equal(t, "ipfix", specs[1].Scheme)
	assert.Equal(t, "sflow", specs[2].Scheme)
	assert.Equal(t, "flow", specs[3].Scheme)

	_, err = parseListeners([]string{"tcp://0.0.0.0:2055"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported")
}
