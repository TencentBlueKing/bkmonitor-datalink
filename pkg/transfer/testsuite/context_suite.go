// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package testsuite

import (
	"context"
	"os"

	"github.com/golang/mock/gomock"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/logging"
)

// ContextSuite :
type ContextSuite struct {
	StubSuite
	Cancel context.CancelFunc
	CTX    context.Context
	Ctrl   *gomock.Controller
}

// SetupTest :
func (s *ContextSuite) SetupTest() {
	s.StubSuite.SetupTest()
	opts := logging.GetOptions()
	if os.Getenv("LOG_DEBUG") == "0" {
		opts.Level = "warn"
	} else {
		opts.Level = "debug"
	}
	logging.SetOptions(opts)
	s.CTX, s.Cancel = context.WithCancel(context.Background())
	s.Ctrl = gomock.NewController(s.T())
}

// TearDownTest :
func (s *ContextSuite) TearDownTest() {
	s.StubSuite.TearDownTest()
	s.Ctrl.Finish()
}
