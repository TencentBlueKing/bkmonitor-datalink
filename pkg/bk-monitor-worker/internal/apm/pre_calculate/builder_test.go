// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package pre_calculate

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/apm/pre_calculate/core"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/apm/pre_calculate/storage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/apm/pre_calculate/window"
)

func TestRunInstanceStartWindowHandlerExclusiveUsesMessageChan(t *testing.T) {
	dataId := "exclusive-data-id"
	core.InitMetadataCenter(&core.MetadataCenter{Mapping: &sync.Map{}})
	core.GetMetadataCenter().AddDataIdAndInfo(dataId, "token-a", core.DataIdInfo{
		BaseInfo: core.BaseInfo{Token: "token-a", BkBizId: "2", AppName: "app-a"},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	messageChan := make(chan []window.StandardSpan)
	defer close(messageChan)

	instance := newTestRunInstance(ctx, dataId)
	instance.startWindowHandler(messageChan, make(chan storage.SaveRequest, 1))

	require.Len(t, instance.appBundles, 1)
	assert.False(t, core.GetMetadataCenter().IsShared(dataId))
	assert.True(t, (<-chan []window.StandardSpan)(messageChan) == instance.appBundles[0].spanChan)
}

func TestRunInstanceStartWindowHandlerSharedUsesDispatcherChan(t *testing.T) {
	dataId := "shared-data-id"
	appA := core.BaseInfo{Token: "token-a", BkBizId: "2", AppName: "app-a"}
	appB := core.BaseInfo{Token: "token-b", BkBizId: "3", AppName: "app-b"}
	core.InitMetadataCenter(&core.MetadataCenter{Mapping: &sync.Map{}})
	core.GetMetadataCenter().AddDataIdAndInfo(dataId, "", core.DataIdInfo{
		IsShared: true,
		Apps: map[core.AppKey]core.BaseInfo{
			appA.AppKey(): appA,
			appB.AppKey(): appB,
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	messageChan := make(chan []window.StandardSpan)
	defer close(messageChan)

	instance := newTestRunInstance(ctx, dataId)
	instance.startWindowHandler(messageChan, make(chan storage.SaveRequest, 1))

	require.Len(t, instance.appBundles, 2)
	assert.True(t, core.GetMetadataCenter().IsShared(dataId))
	for _, bundle := range instance.appBundles {
		assert.False(t, (<-chan []window.StandardSpan)(messageChan) == bundle.spanChan)
	}
}

func TestRunInstanceStartWindowHandlerSharedWithoutApps(t *testing.T) {
	dataId := "shared-empty-apps-data-id"
	core.InitMetadataCenter(&core.MetadataCenter{Mapping: &sync.Map{}})
	core.GetMetadataCenter().AddDataIdAndInfo(dataId, "", core.DataIdInfo{IsShared: true})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	messageChan := make(chan []window.StandardSpan)
	defer close(messageChan)

	instance := newTestRunInstance(ctx, dataId)
	assert.NotPanics(t, func() {
		instance.startWindowHandler(messageChan, make(chan storage.SaveRequest, 1))
	})

	assert.True(t, core.GetMetadataCenter().IsShared(dataId))
	assert.Empty(t, instance.appBundles)
}

func newTestRunInstance(ctx context.Context, dataId string) *RunInstance {
	return &RunInstance{
		ctx:              ctx,
		startInfo:        StartInfo{DataId: dataId},
		errorReceiveChan: make(chan error, 10),
		config: PrecalculateOption{
			distributiveWindowConfig: []window.DistributiveWindowOption{
				window.DistributiveWindowSubSize(1),
				window.DistributiveWindowWatchExpiredInterval(time.Hour),
				window.DistributiveWindowConcurrentProcessCount(1),
				window.DistributiveWindowConcurrentExpirationMaximum(1),
				window.DistributiveWindowMappingMaxSpanCount(1),
			},
			runtimeConfig: []window.RuntimeConfigOption{
				window.RuntimeConfigMaxSize(10),
				window.RuntimeConfigExpireInterval(time.Second),
				window.RuntimeConfigMaxDuration(time.Minute),
				window.ExpireIntervalIncrement(1),
				window.NoDataMaxDuration(time.Minute),
			},
			processorConfig: []window.ProcessorOption{
				window.TraceEsQueryRate(1),
			},
		},
	}
}
