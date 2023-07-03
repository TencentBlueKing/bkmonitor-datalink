// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package processor

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/filesystem"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/logging"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/pipeline"
)

// Frontend :
type Frontend struct {
	*define.BaseFrontend
	*define.ProcessorMonitor
	file     filesystem.File
	lifeTime time.Duration
	done     bool
	ctx      context.Context
}

// NewFrontend :
func NewFrontend(ctx context.Context, name string) define.Frontend {
	conf := config.FromContext(ctx)
	root := conf.GetString(ConfFrontendDirKey)
	pipe := config.PipelineConfigFromContext(ctx)
	file, err := filesystem.FS.OpenFile(filepath.Join(root, strconv.Itoa(pipe.DataID)), os.O_RDONLY, 0)
	if err != nil {
		panic(err)
	}
	return &Frontend{
		BaseFrontend:     define.NewBaseFrontend(name),
		ProcessorMonitor: pipeline.NewFrontendProcessorMonitor(config.PipelineConfigFromContext(ctx)),
		file:             file,
		ctx:              ctx,
	}
}

// Pull : pull data
func (f *Frontend) Pull(outputChan chan<- define.Payload, killChan chan<- error) {
	var err error
	f.done = false
	reader := bufio.NewReader(f.file)

	fInfo, err := f.file.Stat()
	if err != nil {
		logging.Errorf("%v stats error %v", f, err)
		killChan <- err
		return
	}

	count := 0
	logging.Infof("%v pulling from file %s with size %d", f, fInfo.Name(), fInfo.Size())

	startAt := time.Now()
	for !f.done {
		line, err := reader.ReadString('\n')
		if err != nil {
			break
		}

		data := strings.TrimSpace(line)
		if len(data) == 0 {
			continue
		}
		payload := f.PayloadCreator()
		err = payload.From([]byte(data))
		if err != nil {
			logging.Errorf("%v parse data error %v", f, err)
			killChan <- err
			return
		}
		f.CounterSuccesses.Inc()
		count++
		outputChan <- payload
	}

	f.lifeTime = time.Since(startAt)
	logging.Infof("%v pulled %d data in %v", f, count, f.lifeTime)

	err = f.file.Close()
	if err != nil {
		killChan <- err
	}
}

// Close : close frontend
func (f *Frontend) Close() error {
	f.done = true
	return nil
}

func init() {
	define.RegisterFrontend("file", func(ctx context.Context, name string) (define.Frontend, error) {
		return NewFrontend(ctx, name), nil
	})
}
