// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package influxdb_test

import (
	"context"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/prashantv/gostub"
	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/backend"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/backend/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/common"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/mocktest"
)

type IOCloser struct{}

func (c *IOCloser) Close() error {
	return nil
}

func (c *IOCloser) Read(p []byte) (n int, err error) {
	return 0, io.EOF
}

// BackendSuite :
type BackendSuite struct {
	suite.Suite
}

func (bs *BackendSuite) TestUsage() {
	ctrl := gomock.NewController(bs.T())
	defer ctrl.Finish()

	// stub for kafka backup storage
	kStubs := gostub.Stub(&influxdb.NewKafkaBackup, func(ctx context.Context, topicName string) (influxdb.StorageBackup, error) {
		ks := mocktest.NewMockStorageBackup(ctrl)
		ks.EXPECT().HasData().Return(false, nil).MinTimes(1)
		ks.EXPECT().Push(gomock.Any()).Return(nil).MinTimes(1)
		ks.EXPECT().GetOffsetSize().Return(int64(0), nil).MinTimes(1)
		return ks, nil
	})
	defer kStubs.Reset()
	res := &http.Response{
		Body:       new(IOCloser),
		StatusCode: http.StatusNoContent,
	}

	httpClient := mocktest.NewHTTPClient(ctrl)
	httpClient.EXPECT().Do(gomock.Any()).Return(res, nil).AnyTimes()
	kStubs.StubFunc(&influxdb.NewHTTPClient, httpClient)

	info := &backend.Info{
		DomainName: "127.0.0.1",
		Port:       8087,
		Username:   "",
		Password:   "",
	}
	influxInfo := backend.MakeBasicConfig("test", info, false, false, 30*time.Second)
	var err error
	// mock set kafka version
	common.Config.SetDefault(common.ConfigKeyKafkaVersion, "0.10.2.0")
	common.Config.SetDefault(common.ConfigKeyBatchSize, 0)
	common.Config.SetDefault(common.ConfigKeyFlushTime, "5s")
	common.Config.SetDefault(common.ConfigKeyMaxFlushConcurrency, 100)
	influxInstance, _, _ := influxdb.NewBackend(context.Background(), influxInfo)
	bs.Equal(influxInstance.String(), "influxdb_backend[test:127.0.0.1:8087]disabled[false]backup_rate_limit[0]")
	points := []byte(`cpu_summary,bk_biz_id=2,bk_cloud_id=0,bk_supplier_id=0,device_name=cpu1,hostname=VM_1_10_centos,ip=127.0.0.1 idle=1788673.01,iowait=46041.32,stolen=0,system=83036.86,usage=47.47134187529686,user=454551.33 1552898829000000000
cpu_summary,bk_biz_id=2,bk_cloud_id=0,bk_supplier_id=0,device_name=cpu2,hostname=VM_1_10_centos,ip=127.0.0.1 idle=1795464.24,iowait=30038.12,stolen=0,system=83006.4,usage=42.99191374630933,user=464300.79 1552898829000000000
cpu_summary,bk_biz_id=2,bk_cloud_id=0,bk_supplier_id=0,device_name=cpu3,hostname=VM_1_10_centos,ip=127.0.0.1 idle=1807027.32,iowait=23940.69,stolen=0,system=82860.09,usage=44.98991257572465,user=458930.71 1552898829000000000`)
	_, err = influxInstance.Write(0, backend.NewWriteParams("test", "", "", ""), backend.NewPointsReaderWithBytes(points), nil)
	bs.Equal(err, nil)
	_, err = influxInstance.Query(0, backend.NewQueryParams("test", "select * from cpu_summary", "", "", "", ""), nil)
	bs.Equal(err, nil)
	_, err = influxInstance.CreateDatabase(0, backend.NewQueryParams("", "create database test", "", "", "", ""), nil)
	bs.Equal(err, nil)
	bs.Equal(influxInstance.GetVersion(), "")
	time.Sleep(6 * time.Second)
}

func (bs *BackendSuite) TestPushFailed() {
	ctrl := gomock.NewController(bs.T())
	defer ctrl.Finish()

	// stub for kafka backup storage
	kStubs := gostub.Stub(&influxdb.NewKafkaBackup, func(ctx context.Context, topicName string) (influxdb.StorageBackup, error) {
		ks := mocktest.NewMockStorageBackup(ctrl)
		ks.EXPECT().HasData().Return(false, nil).AnyTimes()
		ks.EXPECT().Push(gomock.Any()).Times(1)
		ks.EXPECT().GetOffsetSize().Return(int64(0), nil).AnyTimes()
		return ks, nil
	})
	defer kStubs.Reset()
	res := &http.Response{
		Body:       new(IOCloser),
		StatusCode: http.StatusNoContent,
	}

	httpClient := mocktest.NewHTTPClient(ctrl)
	httpClient.EXPECT().Do(gomock.Any()).Return(res, nil).AnyTimes()
	kStubs.StubFunc(&influxdb.NewHTTPClient, httpClient)
	info := &backend.Info{
		DomainName: "127.0.0.1",
		Port:       8087,
		Username:   "",
		Password:   "",
	}
	influxInfo := backend.MakeBasicConfig("test", info, false, false, 30*time.Second)

	// mock set kafka version
	common.Config.SetDefault(common.ConfigKeyKafkaVersion, "0.10.2.0")
	common.Config.SetDefault(common.ConfigKeyBatchSize, 0)
	common.Config.SetDefault(common.ConfigKeyFlushTime, "5s")
	common.Config.SetDefault(common.ConfigKeyMaxFlushConcurrency, 100)
	influxInstance, _, _ := influxdb.NewBackend(context.Background(), influxInfo)

	points := []byte(`cpu_summary,bk_biz_id=2,bk_cloud_id=0,bk_supplier_id=0,device_name=cpu1,hostname=VM_1_10_centos,ip=127.0.0.1 idle=1788673.01,iowait=46041.32,stolen=0,system=83036.86,usage=47.47134187529686,user=454551.33 1552898829000000000
cpu_summary,bk_biz_id=2,bk_cloud_id=0,bk_supplier_id=0,device_name=cpu2,hostname=VM_1_10_centos,ip=127.0.0.1 idle=1795464.24,iowait=30038.12,stolen=0,system=83006.4,usage=42.99191374630933,user=464300.79 1552898829000000000
cpu_summary,bk_biz_id=2,bk_cloud_id=0,bk_supplier_id=0,device_name=cpu3,hostname=VM_1_10_centos,ip=127.0.0.1 idle=1807027.32,iowait=23940.69,stolen=0,system=82860.09,usage=44.98991257572465,user=458930.71 1552898829000000000`)
	_, err := influxInstance.Write(0, backend.NewWriteParams("test", "", "", ""), backend.NewPointsReaderWithBytes(points), nil)
	bs.Equal(err, nil)

	_ = influxInstance.Close()
	influxInstance.Wait()
}

func (bs *BackendSuite) TestBufferUsage() {
	ctrl := gomock.NewController(bs.T())
	defer ctrl.Finish()

	// stub for kafka backup storage
	kStubs := gostub.Stub(&influxdb.NewKafkaBackup, func(ctx context.Context, topicName string) (influxdb.StorageBackup, error) {
		ks := mocktest.NewMockStorageBackup(ctrl)
		ks.EXPECT().HasData().Return(false, nil).MinTimes(1)
		// ks.EXPECT().Push(gomock.Any()).Return(nil).MinTimes(1)
		ks.EXPECT().GetOffsetSize().Return(int64(0), nil).MinTimes(1)
		return ks, nil
	})
	defer kStubs.Reset()
	res := &http.Response{
		Body:       new(IOCloser),
		StatusCode: http.StatusNoContent,
	}

	httpClient := mocktest.NewHTTPClient(ctrl)
	httpClient.EXPECT().Do(gomock.Any()).Return(res, nil).AnyTimes()
	kStubs.StubFunc(&influxdb.NewHTTPClient, httpClient)

	info := &backend.Info{
		DomainName: "127.0.0.1",
		Port:       8087,
		Username:   "",
		Password:   "",
	}
	influxInfo := backend.MakeBasicConfig("test", info, false, false, 30*time.Second)
	var err error
	// mock set kafka version
	common.Config.SetDefault(common.ConfigKeyKafkaVersion, "0.10.2.0")
	common.Config.SetDefault(common.ConfigKeyBatchSize, 15000)
	common.Config.SetDefault(common.ConfigKeyFlushTime, "5s")
	common.Config.SetDefault(common.ConfigKeyMaxFlushConcurrency, 100)
	influxInstance, _, _ := influxdb.NewBackend(context.Background(), influxInfo)
	bs.Equal(influxInstance.String(), "influxdb_backend[test:127.0.0.1:8087]disabled[false]backup_rate_limit[0]")
	points := []byte(`cpu_summary,bk_biz_id=2,bk_cloud_id=0,bk_supplier_id=0,device_name=cpu1,hostname=VM_1_10_centos,ip=127.0.0.1 idle=1788673.01,iowait=46041.32,stolen=0,system=83036.86,usage=47.47134187529686,user=454551.33 1552898829000000000
cpu_summary,bk_biz_id=2,bk_cloud_id=0,bk_supplier_id=0,device_name=cpu2,hostname=VM_1_10_centos,ip=127.0.0.1 idle=1795464.24,iowait=30038.12,stolen=0,system=83006.4,usage=42.99191374630933,user=464300.79 1552898829000000000
cpu_summary,bk_biz_id=2,bk_cloud_id=0,bk_supplier_id=0,device_name=cpu3,hostname=VM_1_10_centos,ip=127.0.0.1 idle=1807027.32,iowait=23940.69,stolen=0,system=82860.09,usage=44.98991257572465,user=458930.71 1552898829000000000`)
	time.Sleep(5 * time.Second)
	_, err = influxInstance.Write(0, backend.NewWriteParams("test", "", "", ""), backend.NewPointsReaderWithBytes(points), nil)
	bs.Equal(err, nil)
	_, err = influxInstance.Query(0, backend.NewQueryParams("test", "select * from cpu_summary", "", "", "", ""), nil)
	bs.Equal(err, nil)
	_, err = influxInstance.CreateDatabase(0, backend.NewQueryParams("", "create database test", "", "", "", ""), nil)
	bs.Equal(err, nil)
	bs.Equal(influxInstance.GetVersion(), "")
	time.Sleep(6 * time.Second)
}

// TestBackendSuite :
func TestBackendSuite(t *testing.T) {
	suite.Run(t, new(BackendSuite))
}
