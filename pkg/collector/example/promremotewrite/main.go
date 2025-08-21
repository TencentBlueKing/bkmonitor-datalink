// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gogo/protobuf/proto"
	"github.com/golang/snappy"
	"github.com/prometheus/prometheus/prompb"
)

const (
	endpoint = "http://localhost:4318/prometheus/write"
)

var total int

func sendRequest(wr *prompb.WriteRequest) error {
	data, err := proto.Marshal(wr)
	if err != nil {
		return err
	}
	buf := make([]byte, len(data), cap(data))
	compressedData := snappy.Encode(buf, data)

	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(compressedData))
	if err != nil {
		return err
	}

	req.Header.Add("Content-Encoding", "snappy")
	req.Header.Set("Content-Type", "application/x-protobuf")
	req.Header.Set("X-Prometheus-Remote-Write-Version", "0.1.0")

	const token = "Ymtia2JrYmtia2JrYmtiaxUtdLzrldhHtlcjc1Cwfo1u99rVk5HGe8EjT761brGtKm3H4Ran78rWl85HwzfRgw=="

	total++
	switch {
	case total%3 == 0:
		req.Header.Set("X-BK-TOKEN", token)
		log.Printf("count(%d): token from [X-BK-TOKEN] header\n", total)
	case total%2 == 0:
		req.SetBasicAuth("bkmonitor", token)
		log.Printf("count(%d): token from [Basic Auth]\n", total)
	default:
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
		log.Printf("count(%d): token from [Bearer Auth]\n", total)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	log.Printf("status: %d, response => %s\n", resp.StatusCode, string(b))

	return err
}

func makeRequest() *prompb.WriteRequest {
	labels := []prompb.Label{
		{
			Name:  "__name__",
			Value: "gauge_int",
		},
		{
			Name:  "callee_service",
			Value: "hello",
		},
		{
			Name:  "code",
			Value: "ret_100",
		},
	}

	ts := time.Now().UnixMilli()
	var samples []prompb.Sample
	for i := 0; i < 10; i++ {
		samples = append(samples, prompb.Sample{
			Timestamp: ts + int64(10*i),
			Value:     rand.Float64(),
		})
	}

	return &prompb.WriteRequest{
		Timeseries: []prompb.TimeSeries{
			{
				Labels:  labels,
				Samples: samples,
			},
		},
	}
}

func main() {
	ticker := time.NewTicker(time.Second * 3)
	defer ticker.Stop()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	for {
		select {
		case <-ticker.C:
			if err := sendRequest(makeRequest()); err != nil {
				log.Printf("failed to send request: %v\n", err)
				continue
			}

		case <-sigCh:
			return
		}
	}
}
