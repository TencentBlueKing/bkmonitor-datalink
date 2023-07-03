// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package models_test

import (
	"testing"
	"time"

	"github.com/golang/mock/gomock"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/models"
	. "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/testsuite"
)

func newHost() *models.CCHostInfo {
	return &models.CCHostInfo{
		IP:      "127.0.0.1",
		CloudID: 0,
		OuterIP: "8.8.8.8",
	}
}

func benchmarkHostDump(b *testing.B, converter models.Converter) {
	host := newHost()
	ctrl := gomock.NewController(b)
	store := NewMockStore(ctrl)
	store.EXPECT().Set(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(key string, data []byte, expires time.Duration) error {
		b.Logf("%s set length %d", key, len(data))
		return nil
	}).AnyTimes()

	modelConverter := models.ModelConverter
	models.ModelConverter = converter

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		err := host.Dump(store, define.StoreNoExpires)
		if err != nil {
			panic(err)
		}
	}
	b.StopTimer()

	ctrl.Finish()
	models.ModelConverter = modelConverter
}

// BenchmarkHost_Dump_JSON :
func BenchmarkHost_Dump_JSON(b *testing.B) {
	benchmarkHostDump(b, models.JSONConverter{})
}

// BenchmarkHost_Dump_Gob :
func BenchmarkHost_Dump_Gob(b *testing.B) {
	benchmarkHostDump(b, models.GobConverter{})
}

func benchmarkHostLoadByIP(b *testing.B, converter models.Converter) {
	host := newHost()
	hostData, err := converter.Marshal(host)
	if err != nil {
		panic(err)
	}

	ctrl := gomock.NewController(b)
	store := NewMockStore(ctrl)
	store.EXPECT().Get(gomock.Any()).DoAndReturn(func(key string) ([]byte, error) {
		return hostData, nil
	}).AnyTimes()

	modelConverter := models.ModelConverter
	models.ModelConverter = converter

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		err := host.LoadStoreKey(store, models.SetHostKey(host.IP, host.CloudID))
		if err != nil {
			panic(err)
		}
	}
	b.StopTimer()

	ctrl.Finish()
	models.ModelConverter = modelConverter
}

// BenchmarkHost_LoadByIP_JSON :
func BenchmarkHost_LoadByIP_JSON(b *testing.B) {
	benchmarkHostLoadByIP(b, models.JSONConverter{})
}

// BenchmarkHost_LoadByIP_Gob :
func BenchmarkHost_LoadByIP_Gob(b *testing.B) {
	benchmarkHostLoadByIP(b, models.GobConverter{})
}
