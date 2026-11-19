// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package exporter

import (
	"encoding/binary"
	"io"
	"net"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/elastic/beats/libbeat/beat"
	"github.com/elastic/beats/libbeat/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/confengine"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/metrictest"
	bkbeat "github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/beat"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/gse"
	gseoutput "github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/output/gse"
)

const sendFailuresName = "bk_collector_exporter_send_failures_total"

func sendFailure(t *testing.T, reason string) float64 {
	t.Helper()
	v, ok := metrictest.Value(t, sendFailuresName, map[string]string{"reason": reason})
	if !ok {
		t.Errorf("%s has no %s cell", sendFailuresName, reason)
	}
	return v
}

func counter(t *testing.T, name string, labels map[string]string) float64 {
	t.Helper()
	v, _ := metrictest.Value(t, name, labels)
	return v
}

// All four reasons have a cell before anything fails.
func TestSendFailuresCellsExistFromTheStart(t *testing.T) {
	assert.Equal(t,
		[]string{"reason=gse_retry", "reason=gse_write", "reason=oversize", "reason=send_rejected"},
		metrictest.Series(t, sendFailuresName),
	)
}

// A message the GSE client cannot write is retried and then dropped by the
// client itself; its own counters move, and the exporter shows them as
// gse_retry and gse_write.
func TestGseWriteFailuresAreTheGseClientsOwnCounts(t *testing.T) {
	cli, err := gse.NewGseClientFromConfig(gse.Config{
		Endpoint:      filepath.Join(t.TempDir(), "no-agent.sock"),
		RetryTimes:    1,
		RetryInterval: time.Millisecond,
		MsgQueueSize:  1,
	})
	require.NoError(t, err)

	write, retry := sendFailure(t, "gse_write"), sendFailure(t, "gse_retry")
	require.Error(t, cli.SendSync(gse.NewGseDynamicMsg([]byte(`{"k":"v"}`), 1001, 0, 0)))
	assert.Equal(t, write+1, sendFailure(t, "gse_write"))
	assert.Equal(t, retry+1, sendFailure(t, "gse_retry"))
}

// fakeAgent stands in for the GSE agent on a unix socket: it answers the
// client's request for agent info and takes whatever is written to it.
func fakeAgent(t *testing.T) string {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "ipc.state.report")
	ln, err := net.Listen("unix", sock)
	require.NoError(t, err)
	t.Cleanup(func() { ln.Close() })

	info := []byte(`{"bizid":1,"cloudid":0,"ip":"127.0.0.1"}`)
	frame := make([]byte, 8, 8+len(info))
	binary.BigEndian.PutUint32(frame[0:4], gse.GSE_TYPE_GET_CONF)
	binary.BigEndian.PutUint32(frame[4:8], uint32(len(info)))
	frame = append(frame, info...)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				if _, err := conn.Write(frame); err != nil {
					return
				}
				_, _ = io.Copy(io.Discard, conn)
			}()
		}
	}()
	return sock
}

// An encoded message of max_message_bytes or more is not handed to the GSE
// client: it is counted as oversize and not as sent bytes. A smaller one is
// handed on and counted as sent bytes only.
func TestAnEventOfMaxMessageBytesIsDroppedAsOversize(t *testing.T) {
	_, err := New(confengine.MustLoadConfigContent(`
exporter:
  max_message_bytes: 64
`))
	require.NoError(t, err)

	cfg, err := common.NewConfigFrom(map[string]any{
		"endpoint":      fakeAgent(t),
		"retrytimes":    1,
		"retryinterval": "1ms",
	})
	require.NoError(t, err)
	group, err := gseoutput.MakeGSE(nil, beat.Info{}, nil, cfg)
	require.NoError(t, err)
	require.Len(t, group.Clients, 1)
	output := group.Clients[0].(*gseoutput.Output)
	t.Cleanup(func() { output.Close() })

	const dataID = 1501
	id := map[string]string{"id": strconv.Itoa(dataID)}
	oversize := sendFailure(t, "oversize")
	sentBytes := counter(t, "bk_collector_beat_sent_bytes_total", id)

	small := common.MapStr{"k": "v"}
	require.NoError(t, output.ReportCommonData(dataID, small))
	assert.Equal(t, oversize, sendFailure(t, "oversize"))
	assert.Equal(t, sentBytes+float64(len(`{"k":"v"}`)), counter(t, "bk_collector_beat_sent_bytes_total", id))

	large := common.MapStr{"k": strings.Repeat("v", 64)}
	require.NoError(t, output.ReportCommonData(dataID, large))
	assert.Equal(t, oversize+1, sendFailure(t, "oversize"))
	assert.Equal(t, sentBytes+float64(len(`{"k":"v"}`)), counter(t, "bk_collector_beat_sent_bytes_total", id))
}

// proxyEvent is an event of one record type, made as the converters make theirs.
type proxyEvent struct{ define.CommonEvent }

func (proxyEvent) RecordType() define.RecordType { return define.RecordProxy }

// An event the beat pipeline refuses is counted as send_rejected and not as
// sent; one it takes is counted as sent.
func TestAnEventTheBeatRefusesIsNotCountedAsSent(t *testing.T) {
	e, err := New(confengine.MustLoadConfigContent(`
exporter:
  queue:
    flush_interval: 10ms
`))
	require.NoError(t, err)

	accept := make(chan bool, 1)
	sentFunc := SentFunc
	SentFunc = func(bkbeat.MapStr) bool { return <-accept }
	t.Cleanup(func() { SentFunc = sentFunc })
	require.NoError(t, e.Start())
	t.Cleanup(e.Stop)

	publish := func(ok bool) {
		accept <- ok
		PublishEvents(proxyEvent{define.NewCommonEvent(define.Token{}, 1502, common.MapStr{"k": "v"})})
		require.Eventually(t, func() bool { return len(accept) == 0 }, 5*time.Second, 5*time.Millisecond)
	}

	sent := counter(t, "bk_collector_exporter_sent_total", nil)
	rejected := sendFailure(t, "send_rejected")

	publish(false)
	assert.Eventually(t, func() bool { return sendFailure(t, "send_rejected") == rejected+1 }, 5*time.Second, 5*time.Millisecond)
	assert.Equal(t, sent, counter(t, "bk_collector_exporter_sent_total", nil))

	publish(true)
	assert.Eventually(t, func() bool { return counter(t, "bk_collector_exporter_sent_total", nil) == sent+1 }, 5*time.Second, 5*time.Millisecond)
	assert.Equal(t, rejected+1, sendFailure(t, "send_rejected"))
}
