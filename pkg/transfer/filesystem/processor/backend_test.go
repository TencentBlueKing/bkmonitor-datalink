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
	"io"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/filesystem"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/filesystem/processor"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/json"
	. "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/testsuite"
)

// BackendSuite :
type BackendSuite struct {
	ConfigSuite
	fs filesystem.FileSystem
}

// SetupTest :
func (s *BackendSuite) SetupTest() {
	s.ConfigSuite.SetupTest()
	s.fs = filesystem.FS
	filesystem.FS = afero.NewMemMapFs()

	s.Config.Set(processor.ConfBackendFilePermKey, "0644")
	s.Config.Set(processor.ConfBackendDirKey, "")
}

// TearDownTest :
func (s *BackendSuite) TearDownTest() {
	s.ConfigSuite.TearDownTest()
	filesystem.FS = s.fs
}

// TestUsage :
func (s *BackendSuite) TestUsage() {
	value := map[string]interface{}{
		"float":  1.2,
		"string": "3",
	}
	ctrl := gomock.NewController(s.T())
	payload := NewMockPayload(ctrl)
	payload.EXPECT().To(gomock.Any()).DoAndReturn(func(v interface{}) error {
		ptr := v.(*map[string]interface{})
		*ptr = value
		return nil
	})

	var wg sync.WaitGroup
	killCh := make(chan error)

	wg.Add(1)
	go func() {
		for err := range killCh {
			panic(err)
		}
		wg.Done()
	}()

	pipe := config.PipelineConfigFromContext(s.CTX)

	table := config.ResultTableConfigFromContext(s.CTX)
	table.ResultTable = "test"

	shipper := config.ShipperConfigFromContext(s.CTX)
	cluster := shipper.AsInfluxCluster()
	cluster.StorageConfig = map[string]interface{}{
		"database":        "test",
		"real_table_name": "test",
	}
	cluster.ClusterConfig = map[string]interface{}{
		"domain_name": "haha.com",
		"port":        1000,
		"schema":      nil,
	}

	backend := processor.NewBackend(s.CTX, "test")
	backend.Push(payload, killCh)
	close(killCh)
	wg.Wait()
	s.NoError(backend.Close())

	file, err := filesystem.FS.OpenFile(filepath.Join(strconv.Itoa(pipe.DataID), table.ResultTable), os.O_RDONLY, 0)
	s.NoError(err)
	data, err := io.ReadAll(file)
	s.NoError(err)
	var result map[string]interface{}
	s.NoError(json.Unmarshal(data, &result))

	for key := range value {
		s.Equal(value[key], result[key])
	}
	ctrl.Finish()
}

// TestBackendSuite :
func TestBackendSuite(t *testing.T) {
	suite.Run(t, new(BackendSuite))
}
