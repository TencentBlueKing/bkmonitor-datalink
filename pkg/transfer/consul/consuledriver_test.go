// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package consul_test

import (
	"context"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/prashantv/gostub"
	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/consul"
	. "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/testsuite"
)

// SourceDriverSuite :
type SourceDriverSuite struct {
	ConfigSuite
}

// SetupTest :
func (sds *SourceDriverSuite) SetupTest() {
	sds.ConfigSuite.SetupTest()
	sds.Config.Set(consul.ConfKeyDataIDPath, "metadata/data_id")
}

// TestStartConsulDriver :
func (sds SourceDriverSuite) TestStartConsulDriver() {
	ctrl := gomock.NewController(sds.T())
	defer ctrl.Finish()

	subPath := consul.GetPathByKey(sds.Config, consul.ConfKeyDataIDPath)
	dataID1Path := subPath + "1"
	dataID2Path := subPath + "2"

	sc := NewMockSourceClient(ctrl)
	sc.EXPECT().Get(gomock.Eq(dataID1Path)).Return([]byte("123"), nil).AnyTimes()
	sc.EXPECT().Get(gomock.Eq(dataID2Path)).Return([]byte("234"), nil).AnyTimes()
	consulEvent := consul.NewConsulEvent()
	var ceItem consul.EventItem
	ceItem.EventType = config.EventAdded
	ceItem.DataPath = dataID1Path
	ceItem.DataValue = []byte("123")
	consulEvent.Detail = append(consulEvent.Detail, ceItem)
	ceItem.DataPath = dataID2Path
	ceItem.DataValue = []byte("234")
	consulEvent.Detail = append(consulEvent.Detail, ceItem)
	sc.EXPECT().MonitorPath(gomock.Any()).DoAndReturn(func(conPaths []string) (<-chan *consul.Event, error) {
		ch := make(chan *consul.Event)
		go func() {
			ch <- consulEvent
			close(ch)
		}()
		return ch, nil
	})

	stubs := gostub.Stub(&consul.NewConsulClient, func(ctx context.Context) (consul.SourceClient, error) {
		return sc, nil
	})
	defer stubs.Reset()
	ch, err := consul.StartMonitorDataID(sds.CTX)
	sds.NoError(err)

	for cfgEvent := range ch {
		sds.Equal(len(cfgEvent.Detail), 2)
		// maybe index 0 -> 1, or index 0 -> 2 unordered
		if cfgEvent.Detail[0].DataPath == dataID1Path {
			// order 1,2
			sds.Equal(cfgEvent.Detail[0].EventType, config.EventAdded)
			sds.Equal(cfgEvent.Detail[0].DataValue, []byte("123"))
			sds.Equal(cfgEvent.Detail[1].DataPath, dataID2Path)
			sds.Equal(cfgEvent.Detail[1].EventType, config.EventAdded)
			sds.Equal(cfgEvent.Detail[1].DataValue, []byte("234"))
		} else {
			// order 2,1
			sds.Equal(cfgEvent.Detail[1].EventType, config.EventAdded)
			sds.Equal(cfgEvent.Detail[1].DataValue, []byte("123"))
			sds.Equal(cfgEvent.Detail[0].DataPath, dataID2Path)
			sds.Equal(cfgEvent.Detail[0].EventType, config.EventAdded)
			sds.Equal(cfgEvent.Detail[0].DataValue, []byte("234"))
		}
	}
}

// TestSourceDriver :
func TestSourceDriver(t *testing.T) {
	suite.Run(t, new(SourceDriverSuite))
}
