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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/elastic/beats/libbeat/beat"
	"github.com/elastic/beats/libbeat/outputs/codec"
	"github.com/prometheus/client_golang/prometheus"
)

type mockClient struct {
	bs    bytes.Buffer
	enc   codec.Codec
	index string
}

func (c *mockClient) Close() error {
	return nil
}

func (c *mockClient) Publish(event Event) {
	bs, err := c.enc.Encode(c.index, &event)
	if err != nil {
		return
	}
	_ = json.Indent(&c.bs, bs, "", "  ")
}

func (c *mockClient) PublishAll(events []Event) {
	for _, event := range events {
		c.Publish(event)
	}
}

func newMockClient(b beat.Info) *mockClient {
	c := &mockClient{}
	c.index = b.Beat
	c.enc, _ = codec.CreateEncoder(b, codec.Config{})
	return c
}

func isValueEqual(v1, v2 interface{}) error {
	switch vv1 := v1.(type) {
	case map[string]interface{}:
		vv2, ok := v2.(map[string]interface{})
		if !ok {
			return fmt.Errorf("v1: %+v, v2: %+v not same type: %T vs %T", v1, v2, v1, v2)
		}
		if len(vv1) != len(vv2) {
			return fmt.Errorf("v1: %+v, v2: %+v not same length: %d vs %d", v1, v2, len(vv1), len(vv2))
		}
		for k1, vvv1 := range vv1 {
			if err := isValueEqual(vvv1, vv2[k1]); err != nil {
				return err
			}
		}
		return nil
	case []interface{}:
		vv2, ok := v2.([]interface{})
		if !ok {
			return fmt.Errorf("v1: %+v, v2: %+v not same type: %T vs %T", v1, v2, v1, v2)
		}
		if len(vv1) != len(vv2) {
			return fmt.Errorf("v1: %+v, v2: %+v not same length: %d vs %d", v1, v2, len(vv1), len(vv2))
		}
		for i1, vvv1 := range vv1 {
			if err := isValueEqual(vvv1, vv2[i1]); err != nil {
				return err
			}
		}
		return nil
	default:
		if v1 != v2 {
			return fmt.Errorf("v1: %+v, v2: %+v not equal", v1, v2)
		}
		return nil
	}
}

func isJsonEqual(s1, s2 string) error {
	var m1, m2 map[string]interface{}
	err := json.Unmarshal([]byte(s1), &m1)
	if err != nil {
		return fmt.Errorf("s1 unmarshal failed("+s1+"): %w", err)
	}
	err = json.Unmarshal([]byte(s2), &m2)
	if err != nil {
		return fmt.Errorf("s2 unmarshal failed("+s2+"): %w", err)
	}
	return isValueEqual(m1, m2)
}

func Test_gsePusher_Push(t *testing.T) {
	type fields struct {
		p Pusher
		c *mockClient
	}
	now := nowFunc()
	nameCounter := "test_m_counter"
	mCounter := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: nameCounter,
	}, []string{
		"a", "b", "c",
	})
	valueCounter := 100.0
	mCounter.With(prometheus.Labels{
		"a": "a1",
		"b": "b2",
		"c": "c3",
	}).Add(valueCounter)
	d1 := fmt.Sprintf(`
      {
         "dimension":{
            "a":"a1",
            "b":"b2",
            "c":"c3",
            "l1": "v1",
            "l2": "v2",
            "l3": "v3"
         },
         "metrics":{
            "%s":%v
         },
         "timestamp":%d
      }
`, nameCounter, valueCounter, now.UnixNano()/1e6)
	nameHis := "test_m_his"
	mHis := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    nameHis,
		Buckets: []float64{1},
	}, []string{
		"d", "e",
	})
	valueHis := 5.
	mHis.With(prometheus.Labels{
		"d": "d2",
		"e": "e2",
	}).Observe(valueHis)
	d2 := fmt.Sprintf(`
      {
         "dimension":{
            "d":"d2",
            "e":"e2",
            "le":"1",
            "l1": "v1",
            "l2": "v2",
            "l3": "v3"
         },
         "metrics":{
            "%s_bucket":0
         },
         "timestamp":%d
      },
      {
         "dimension":{
            "d":"d2",
            "e":"e2",
            "le":"+Inf",
            "l1": "v1",
            "l2": "v2",
            "l3": "v3"
         },
         "metrics":{
            "%s_bucket":1
         },
         "timestamp":%d
      },
      {
         "dimension":{
            "d":"d2",
            "e":"e2",
            "l1": "v1",
            "l2": "v2",
            "l3": "v3"
         },
         "metrics":{
            "%s_count":1
         },
         "timestamp":%d
      },
      {
         "dimension":{
            "d":"d2",
            "e":"e2",
            "l1": "v1",
            "l2": "v2",
            "l3": "v3"
         },
         "metrics":{
            "%s_sum":%f
         },
         "timestamp":%d
      }
`,
		nameHis, now.UnixNano()/1e6,
		nameHis, now.UnixNano()/1e6,
		nameHis, now.UnixNano()/1e6,
		nameHis, valueHis, now.UnixNano()/1e6,
	)
	b := beat.Info{
		Beat:    "test_beat",
		Version: "1.0",
	}
	nowTime := time.Now()
	nowFunc = func() time.Time {
		return nowTime
	}
	ts := now.Unix()
	dataID := 123
	c := newMockClient(b)
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `{"l3": "v3"}`)
	}))
	defer testServer.Close()
	tests := []struct {
		name   string
		fields fields
		want   string
	}{
		{
			name: "t1",
			fields: fields{
				p: NewGsePusher(context.Background(), &PusherConfig{
					DataID: 123,
					Labels: []map[string]string{
						{"l1": "v1", "l2": "v1", "l3": "v1"},
					},
					RemoteLabelsURL: testServer.URL,
				}).Client(c).Collector(mCounter).Collector(mHis).ConstLabels(map[string]string{"l2": "v2"}),
			},
			want: fmt.Sprintf(`
{
   "@timestamp":"%s",
   "@metadata":{
      "beat":"%s",
      "type":"_doc",
      "version":"%s"
   },
   "dataid":%d,
   "time":%d,
   "data":[
      %s,
      %s
    ],
   "timestamp":%d
}
`, now.UTC().Format("2006-01-02T15:04:05.000Z"), b.Beat, b.Version, dataID, ts, d1, d2, ts),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.fields.p.Push(); err != nil {
				t.Errorf("Push() error = %v", err)
			}
			got := c.bs.String()
			err := isJsonEqual(got, tt.want)
			if err != nil {
				t.Errorf("Push() isJsonEqual err: %v", err)
			}
		})
	}
}
