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
	"context"
	"log"
	"math/rand"
	"net/http"
	"runtime/pprof"
	"time"

	"connectrpc.com/connect"

	pushv1 "github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/receiver/pyroscope/gen/proto/go/push/v1"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/receiver/pyroscope/gen/proto/go/push/v1/pushv1connect"
	typesv1 "github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/receiver/pyroscope/gen/proto/go/types/v1"
)

func collectCpuProfile() ([]byte, error) {
	var buf bytes.Buffer
	if err := pprof.StartCPUProfile(&buf); err != nil {
		log.Println(err)
		return nil, err
	}

	// random wait [1, 10] seconds
	waitSecond := time.Duration(rand.Intn(10)) + 1
	time.Sleep(time.Second * waitSecond)
	pprof.StopCPUProfile()

	return buf.Bytes(), nil
}

func main() {
	client := pushv1connect.NewPusherServiceClient(
		http.DefaultClient,
		"http://localhost:4318",
	)
	cpuProfile, err := collectCpuProfile()
	if err != nil {
		log.Println(err)
		return
	}
	req := connect.NewRequest(&pushv1.PushRequest{
		Series: []*pushv1.RawProfileSeries{
			{
				Labels: []*typesv1.LabelPair{
					{Name: "service_name", Value: "test_service"},
					{Name: "env", Value: "test"},
				},
				Samples: []*pushv1.RawSample{
					{
						RawProfile: cpuProfile,
					},
				},
			},
		},
	})
	req.Header().Set("X-BK-TOKEN", "Ymtia2JrYmtia2JrYmtiaxUtdLzrldhHtlcjc1Cwfo1u99rVk5HGe8EjT761brGtKm3H4Ran78rWl85HwzfRgw==")
	res, err := client.Push(
		context.Background(),
		req,
	)
	if err != nil {
		log.Println(err)
		return
	}
	log.Println(res.Msg)
}
