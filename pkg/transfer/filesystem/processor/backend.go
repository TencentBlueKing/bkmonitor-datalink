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
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/filesystem"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/logging"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/pipeline"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

// Backend :
type Backend struct {
	*define.BaseBackend
	*define.ProcessorMonitor
	*pipeline.ProcessorTimeObserver

	file filesystem.File
	ctx  context.Context
}

// NewBackend :
func NewBackend(ctx context.Context, name string) define.Backend {
	table := config.ResultTableConfigFromContext(ctx)
	if table == nil {
		logging.Panic("result table config is empty")
	}

	conf := config.FromContext(ctx)
	pipe := config.PipelineConfigFromContext(ctx)
	shipper := config.ShipperConfigFromContext(ctx)
	root := filepath.Join(conf.GetString(ConfBackendDirKey), strconv.Itoa(pipe.DataID))
	perm, err := utils.StringToFilePerm(conf.GetString(ConfBackendFilePermKey))
	logging.PanicIf(err)
	file, err := filesystem.FS.OpenFile(filepath.Join(root, table.ResultTable), os.O_APPEND|os.O_CREATE|os.O_WRONLY, perm)
	logging.PanicIf(err)

	return &Backend{
		BaseBackend:           define.NewBaseBackend(name),
		ProcessorMonitor:      pipeline.NewBackendProcessorMonitor(pipe, shipper),
		ProcessorTimeObserver: pipeline.NewProcessorTimeObserver(pipe),
		file:                  file,
		ctx:                   ctx,
	}
}

// Push : Push data
func (b *Backend) Push(d define.Payload, killChan chan<- error) {
	var data map[string]interface{}
	utils.CheckError(d.To(&data))
	bytes, err := json.Marshal(data)
	if err != nil {
		killChan <- err
	}
	jsonData := string(bytes)
	logging.Debugf("backend[%s] received payload: %s", b.Name, jsonData)
	_, err = fmt.Fprintln(b.file, jsonData)
	b.CounterSuccesses.Inc()
	utils.CheckError(err)
}

// Close : close backend
func (b *Backend) Close() error {
	return b.file.Close()
}

func (b *Backend) SetETLRecordFields(f *define.ETLRecordFields) {}

func init() {
	define.RegisterBackend("file", func(ctx context.Context, name string) (define.Backend, error) {
		return NewBackend(ctx, name), nil
	})
}
