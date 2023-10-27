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
	"fmt"
	"strconv"
	"sync"
	"testing"

	"github.com/spf13/afero"
	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/filesystem"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/filesystem/processor"
)

// FrontendSuite :
type FrontendSuite struct {
	suite.Suite
	conf define.Configuration
	fs   filesystem.FileSystem
}

// SetupTest :
func (s *FrontendSuite) SetupTest() {
	s.conf = config.Configuration
	s.fs = filesystem.FS
	filesystem.FS = afero.NewMemMapFs()
}

// TearDownTest :
func (s *FrontendSuite) TearDownTest() {
	config.Configuration = s.conf
	filesystem.FS = s.fs
}

// TestUsage :
func (s *FrontendSuite) TestUsage() {
	pipeConfig := new(config.PipelineConfig)
	pipeConfig.DataID = 0
	cases := []struct {
		value interface{}
		data  string
	}{
		{"1", "{\"v\": \"1\"}"},
		{"2", "{\"v\": \"2\"}"},
		{3.4, "{\"v\": 3.4}"},
		{"", "\n\n{\"v\": \"\"}\n\n"},
		{nil, "{\"v\": null}"},
	}
	file, err := filesystem.FS.Create(strconv.Itoa(pipeConfig.DataID))
	s.NoError(err)

	for _, c := range cases {
		_, err = file.WriteString(fmt.Sprintf("%s\n", c.data))
		s.NoError(err)
	}
	s.NoError(file.Close())

	var wg sync.WaitGroup
	outCh := make(chan define.Payload)
	killCh := make(chan error)
	conf := config.NewConfiguration()
	ctx := config.IntoContext(context.Background(), conf)
	ctx = config.PipelineConfigIntoContext(ctx, pipeConfig)

	f := processor.NewFrontend(ctx, "test")

	wg.Add(1)
	go func() {
		for err := range killCh {
			panic(err)
		}
		wg.Done()
	}()

	wg.Add(1)
	go func() {
		f.Pull(outCh, killCh)
		s.NoError(f.Close())
		close(outCh)
		close(killCh)
		wg.Done()
	}()

	index := 0
	for out := range outCh {
		value := make(map[string]interface{})
		s.NoError(out.To(&value))
		s.Equal(cases[index].value, value["v"])
		index++
	}
	wg.Wait()
}

// TestFrontend :
func TestFrontend(t *testing.T) {
	suite.Run(t, new(FrontendSuite))
}
