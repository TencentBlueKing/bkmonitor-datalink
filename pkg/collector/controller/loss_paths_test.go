// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controller

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/elastic/beats/libbeat/beat"
	"github.com/elastic/beats/libbeat/common"
	"github.com/elastic/beats/libbeat/publisher"
	"github.com/google/pprof/profile"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/pdata/ptrace"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/confengine"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/exporter"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/generator"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/metrictest"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/receiver/otlp"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/receiver/pyroscope"
	bkbeat "github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/beat"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/gse"
	gseoutput "github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/output/gse"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/storage"
)

// The cases below follow a request from the handler its route calls, through
// the controller the collector builds and starts (New, Start) from its own
// configuration, to the counter that says the data was lost. The receivers'
// listeners are off - each case calls the handler they route to - and so are
// the proxy, the ping server and the pusher; everything after the handler is
// the collector's own.

const lossToken = "loss-paths"

// lossConfig is a collector configuration with one fixed token, a traces
// deriver and a pprof translator, the exporter flushing every 10ms and
// dropping messages of maxMessageBytes or more, and the pipelines given.
func lossConfig(maxMessageBytes int, pipelines string) string {
	return fmt.Sprintf(`
logging:
  stdout: true
  level: error
receiver:
  disabled: true
proxy:
  disabled: true
pingserver:
  disabled: true
bk_metrics_pusher:
  disabled: true
cache: []
exporter:
  max_message_bytes: %d
  queue:
    flush_interval: 10ms
apm:
  patterns: []
processor:
  - name: "token_checker/fixed"
    config:
      type: "fixed"
      fixed_token: %q
      traces_dataid: 7101
      metrics_dataid: 7102
      profiles_dataid: 7103
      bk_biz_id: 2
      bk_app_name: %q
  - name: "traces_deriver/duration"
    config:
      operations:
        - type: "duration"
          metric_name: "bk_apm_duration"
          rules:
            - kind: "SPAN_KIND_SERVER"
              predicate_key: "attributes.http.method"
              dimensions: ["span_name"]
  - name: "pprof_translator/common"
    config:
      type: "spy"
pipeline:
%s`, maxMessageBytes, lossToken, lossToken, pipelines)
}

const (
	derivingTraces = `
  - name: "traces_pipeline/common"
    type: "traces"
    processors: ["token_checker/fixed", "traces_deriver/duration"]
`
	plainTraces = `
  - name: "traces_pipeline/common"
    type: "traces"
    processors: ["token_checker/fixed"]
`
	derivedMetrics = `
  - name: "metrics_derived_pipeline/common"
    type: "metrics.derived"
    processors: []
`
	translatedProfiles = `
  - name: "profiles_pipeline/common"
    type: "profiles"
    processors: ["token_checker/fixed", "pprof_translator/common"]
`
)

// startCollector builds the collector from content and starts it, stopped
// when the case ends.
func startCollector(t *testing.T, content string) {
	t.Helper()
	c, err := New(confengine.MustLoadConfigContent(content), define.BuildInfo{})
	require.NoError(t, err)
	require.NoError(t, c.Start())
	t.Cleanup(func() { require.NoError(t, c.Stop()) })
}

// postTraces sends spans of one server span each, with attributes, to the
// OTLP HTTP traces handler, and requires it to take them.
func postTraces(t *testing.T, attributes map[string]string) {
	t.Helper()
	traces := generator.NewTracesGenerator(define.TracesOptions{
		GeneratorOptions: define.GeneratorOptions{Attributes: attributes},
		SpanCount:        1,
		SpanKind:         int(ptrace.SpanKindServer),
	}).Generate()
	body, err := ptrace.NewProtoMarshaler().MarshalTraces(traces)
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "http://localhost/v1/traces", bytes.NewReader(body))
	req.Header.Set("Content-Type", define.ContentTypeProtobuf)
	rw := httptest.NewRecorder()
	otlp.HttpService{}.ExportTraces(rw, req)
	require.Equal(t, http.StatusOK, rw.Code, rw.Body.String())
}

// postProfile sends body as a pprof profile to the pyroscope ingest handler,
// and requires it to take it.
func postProfile(t *testing.T, body []byte) {
	t.Helper()
	url := "http://localhost/pyroscope/ingest?name=loss-paths&from=1700000000&until=1700000010&format=pprof&spyName=gospy"
	req := httptest.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	req.Header.Set(define.KeyToken, lossToken)
	rw := httptest.NewRecorder()
	pyroscope.HttpService{}.ProfilesIngest(rw, req)
	require.Equal(t, http.StatusOK, rw.Code, rw.Body.String())
}

func value(t *testing.T, name string, labels map[string]string) float64 {
	t.Helper()
	v, _ := metrictest.Value(t, name, labels)
	return v
}

func eventually(t *testing.T, what string, done func() bool) {
	t.Helper()
	require.Eventually(t, done, 10*time.Second, 10*time.Millisecond, what)
}

// A deployment whose traces pipeline derives metrics but that has no pipeline
// for derived metrics loses every derived record at dispatch: the traces
// request is taken, and each derived record is counted once as no_pipeline
// under the derived protocol, not under the request's. With the pipeline
// configured the derived record is exported and nothing is counted.
func TestADerivedRecordWithNoPipelineIsCountedFromTheRequest(t *testing.T) {
	// A derived record is exported as metrics events under the token's metrics
	// data id.
	derived := map[string]string{"record_type": define.RecordMetrics.S(), "id": "7102"}
	// A server span with an http method, which the deriver derives from.
	httpSpan := map[string]string{"http.method": "GET"}
	noPipeline := func(protocol define.RequestType) float64 {
		return value(t, "bk_collector_receiver_rejected_total", map[string]string{"protocol": protocol.S(), "reason": "no_pipeline"})
	}

	t.Run("no pipeline for derived metrics", func(t *testing.T) {
		startCollector(t, lossConfig(0, derivingTraces))
		lost, viaHTTP := noPipeline(define.RequestDerived), noPipeline(define.RequestHttp)
		exported := value(t, "bk_collector_exporter_handled_event_total", derived)

		postTraces(t, httpSpan)
		eventually(t, "the derived record is counted no_pipeline", func() bool { return noPipeline(define.RequestDerived) == lost+1 })
		postTraces(t, httpSpan)
		eventually(t, "each derived record is counted", func() bool { return noPipeline(define.RequestDerived) == lost+2 })
		assert.Equal(t, viaHTTP, noPipeline(define.RequestHttp))
		assert.Equal(t, exported, value(t, "bk_collector_exporter_handled_event_total", derived))
	})

	t.Run("a pipeline for derived metrics", func(t *testing.T) {
		startCollector(t, lossConfig(0, derivingTraces+derivedMetrics))
		lost := noPipeline(define.RequestDerived)
		exported := value(t, "bk_collector_exporter_handled_event_total", derived)

		postTraces(t, httpSpan)
		eventually(t, "the derived record is exported", func() bool {
			return value(t, "bk_collector_exporter_handled_event_total", derived) > exported
		})
		assert.Equal(t, lost, noPipeline(define.RequestDerived))
	})
}

// A profile the pyroscope handler takes but the pprof translator cannot parse
// is dropped by the pipeline, counted under its pipeline, record type, data
// id and the translator, and not handled; a profile it parses is handled and
// not counted.
func TestAProfileTheTranslatorCannotParseIsCountedDropped(t *testing.T) {
	startCollector(t, lossConfig(0, translatedProfiles))
	labels := map[string]string{"pipeline": "profiles_pipeline/common", "record_type": define.RecordProfiles.S(), "id": "7103"}
	droppedLabels := map[string]string{"processor": "pprof_translator/common"}
	handledLabels := map[string]string{"token": lossToken}
	for k, v := range labels {
		droppedLabels[k], handledLabels[k] = v, v
	}
	dropped := func() float64 { return value(t, "bk_collector_pipeline_dropped_total", droppedLabels) }
	handled := func() float64 { return value(t, "bk_collector_pipeline_handled_total", handledLabels) }
	before, handledBefore := dropped(), handled()

	postProfile(t, []byte("not a profile"))
	eventually(t, "the profile is counted dropped", func() bool { return dropped() == before+1 })
	assert.Equal(t, handledBefore, handled())

	var valid bytes.Buffer
	require.NoError(t, (&profile.Profile{
		SampleType: []*profile.ValueType{{Type: "samples", Unit: "count"}},
		PeriodType: &profile.ValueType{Type: "cpu", Unit: "nanoseconds"},
		Period:     1,
	}).Write(&valid))
	postProfile(t, valid.Bytes())
	eventually(t, "the parsed profile is handled", func() bool { return handled() == handledBefore+1 })
	assert.Equal(t, before+1, dropped())
}

// A span batch whose encoded message reaches max_message_bytes is dropped
// before the GSE client and counted as oversize, its bytes not counted as
// sent; a smaller one is sent. The beat pipeline between the exporter and
// the GSE output is libbeat's queue, stood in for by handing the exporter's
// message to the GSE output as one beat event, the shape the beat makes of
// it (bkbeat.Send); the GSE output, its client and the agent socket's
// handshake are the real ones, against a stand-in agent.
func TestASpanBatchOfMaxMessageBytesIsDroppedAsOversize(t *testing.T) {
	startCollector(t, lossConfig(64*1024, plainTraces))
	output := gseOutput(t)
	sentFunc := exporter.SentFunc
	exporter.SentFunc = func(event bkbeat.MapStr) bool {
		assert.NoError(t, output.PublishEvent(&publisher.Event{Content: beat.Event{Fields: common.MapStr(event), Timestamp: time.Now()}}))
		return true
	}
	t.Cleanup(func() { exporter.SentFunc = sentFunc })

	oversize := func() float64 {
		return value(t, "bk_collector_exporter_send_failures_total", map[string]string{"reason": "oversize"})
	}
	sentBytes := func() float64 { return value(t, "bk_collector_beat_sent_bytes_total", map[string]string{"id": "7101"}) }
	before, bytesBefore := oversize(), sentBytes()

	postTraces(t, map[string]string{"payload": "x"})
	eventually(t, "the small batch is sent", func() bool { return sentBytes() > bytesBefore })
	assert.Equal(t, before, oversize())

	bytesBefore = sentBytes()
	postTraces(t, map[string]string{"payload": strings.Repeat("x", 128*1024)})
	eventually(t, "the large batch is counted oversize", func() bool { return oversize() == before+1 })
	assert.Equal(t, bytesBefore, sentBytes())
}

// gseOutput is the GSE output the beat builds, with the local storage the beat
// initializes for its gse index, against a stand-in agent on a unix socket
// that answers the client's request for agent info and takes whatever is
// written to it.
func gseOutput(t *testing.T) *gseoutput.Output {
	t.Helper()
	require.NoError(t, storage.Init(filepath.Join(t.TempDir(), "gse.db"), nil))
	t.Cleanup(storage.Close)
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

	cfg, err := common.NewConfigFrom(map[string]any{"endpoint": sock, "retrytimes": 1, "retryinterval": "1ms"})
	require.NoError(t, err)
	group, err := gseoutput.MakeGSE(nil, beat.Info{}, nil, cfg)
	require.NoError(t, err)
	require.Len(t, group.Clients, 1)
	output := group.Clients[0].(*gseoutput.Output)
	t.Cleanup(func() { output.Close() })
	return output
}
