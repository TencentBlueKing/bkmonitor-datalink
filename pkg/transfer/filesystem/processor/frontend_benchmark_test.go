// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package processor_test

import (
	"context"
	"os"
	"sync"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/filesystem/processor"
)

// BenchmarkPull :
func BenchmarkPull(b *testing.B) {
	b.StartTimer()
	defer b.StopTimer()

	var wg sync.WaitGroup
	outCh := make(chan define.Payload)
	killCh := make(chan error)
	name := os.Getenv("FRONTEND_NAME")

	b.Logf("file: %s\n", name)
	conf := config.NewConfiguration()
	ctx := config.IntoContext(context.Background(), conf)
	f := processor.NewFrontend(ctx, name)

	wg.Add(1)
	go func() {
		for err := range killCh {
			b.Error(err)
		}
		b.Logf("check killCh done\n")
		wg.Done()
	}()

	wg.Add(1)
	go func() {
		f.Pull(outCh, killCh)
		err := f.Close()
		if err != nil {
			b.Error(err)
		}
		close(outCh)
		close(killCh)
		b.Logf("pull done\n")
		wg.Done()
	}()

	for out := range outCh {
		data := make(map[string]interface{})
		err := out.To(&data)
		if err != nil {
			b.Error(err)
			b.Logf("invalid: %s\n", string(out.(*define.JSONPayload).Data))
		}
		b.Logf("out chan received: %+v\n", data)
	}
	b.Logf("check outCh done\n")

	wg.Wait()
	b.Logf("frontend closed\n")
}
