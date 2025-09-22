// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package influxdb

import (
	"context"
	"sync"
	"time"

	"github.com/prometheus/common/model"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/errno"
	inner "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
)

// 服务侧初始化flux实例使用
type Service struct {
	ctx        context.Context
	cancelFunc context.CancelFunc
	wg         *sync.WaitGroup

	storageHash string
	tableHash   string
	routerHash  string
	metricHash  string
}

// Type
func (s *Service) Type() string {
	return "influxdb"
}

// Start
func (s *Service) Start(ctx context.Context) {
	s.Reload(ctx)
}

// Reload
func (s *Service) Reload(ctx context.Context) {
	var err error
	if s.wg == nil {
		s.wg = new(sync.WaitGroup)
	}
	// 关闭上一次的服务
	if s.cancelFunc != nil {
		s.cancelFunc()
	}

	log.Debugf(ctx, "waiting for influxdb service close")
	// 等待上一次服务结束
	s.Wait()

	// 更新上下文控制方法
	s.ctx, s.cancelFunc = context.WithCancel(ctx)
	log.Debugf(ctx, "influxdb service context update success.")

	err = s.loopReloadStorage(s.ctx)
	if err != nil {
		codedErr := errno.ErrConfigReloadFailed().
			WithComponent("InfluxDB").
			WithOperation("存储配置重载").
			WithError(err)
		log.ErrorWithCodef(ctx, codedErr)
	}

	err = s.loopReloadTableInfo(s.ctx)
	if err != nil {
		codedErr := errno.ErrConfigReloadFailed().
			WithComponent("InfluxDB").
			WithOperation("表信息重载").
			WithError(err)
		log.ErrorWithCodef(ctx, codedErr)
	}

	err = s.loopReloadRouter(s.ctx)
	if err != nil {
		codedErr := errno.ErrConfigReloadFailed().
			WithComponent("InfluxDB").
			WithOperation("查询路由重载").
			WithError(err)
		log.ErrorWithCodef(ctx, codedErr)
	}

	err = s.loopReloadBCSInfo(s.ctx)
	if err != nil {
		codedErr := errno.ErrConfigReloadFailed().
			WithComponent("BCS").
			WithOperation("BCS信息重载").
			WithError(err)
		log.ErrorWithCodef(ctx, codedErr)
	}

	err = s.loopReloadDownsampledInfo(s.ctx)
	if err != nil {
		codedErr := errno.ErrConfigReloadFailed().
			WithComponent("InfluxDB").
			WithOperation("降采样配置重载").
			WithError(err)
		log.ErrorWithCodef(ctx, codedErr)
	}

	err = s.reloadInfluxDBRouter(s.ctx)
	if err != nil {
		codedErr := errno.ErrConfigReloadFailed().
			WithComponent("InfluxDB路由").
			WithOperation("配置加载").
			WithError(err).
			WithSolution("检查InfluxDB路由配置")
		log.ErrorWithCodef(ctx, codedErr)
	}

	err = s.reloadSpaceTsDbRouter(s.ctx)
	if err != nil {
		codedErr := errno.ErrConfigReloadFailed().
			WithComponent("空间TSDB路由").
			WithOperation("配置加载").
			WithError(err).
			WithSolution("检查TSDB路由配置")
		log.ErrorWithCodef(ctx, codedErr)
	}

	log.Infof(ctx, "%s [INFO] | 操作: InfluxDB服务启动 | 状态: 成功 | 说明: 服务已就绪", "InfluxDB服务就绪")
}

// Wait
func (s *Service) Wait() {
	s.wg.Wait()
}

// Close
func (s *Service) Close() {
	s.cancelFunc()
	log.Infof(context.TODO(), "influxdb service context cancel func called.")
}

// reloadTableInfo
func (s *Service) reloadTableInfo() error {
	newData, err := consul.GetInfluxdbTableInfo()
	if err != nil {
		codedErr := errno.ErrConfigReloadFailed().
			WithComponent("InfluxDB").
			WithOperation("从Consul获取表信息").
			WithError(err).
			WithSolution("检查Consul连接和表配置")
		log.ErrorWithCodef(context.TODO(), codedErr)
		return err
	}
	hash := consul.HashIt(newData)
	if hash == s.tableHash {
		log.Debugf(context.TODO(), "table hash not changed")
		return err
	}
	inner.SetTablesInfo(newData)
	s.tableHash = hash
	return nil
}

// reloadStorage
func (s *Service) reloadStorage() error {
	var (
		timeout time.Duration
		dTmp    model.Duration
		err     error
	)
	newData, err := consul.GetInfluxdbStorageInfo()
	if err != nil {
		codedErr := errno.ErrStorageConnFailed().
			WithComponent("InfluxDB").
			WithOperation("存储连接").
			WithError(err).
			WithSolution("检查Consul连接和InfluxDB存储配置")
		log.ErrorWithCodef(context.TODO(), codedErr)
		return err
	}
	hash := consul.HashIt(newData)
	if hash == s.storageHash {
		log.Debugf(context.TODO(), "storage hash not changed")
		return err
	}
	dTmp, err = model.ParseDuration(Timeout)
	if err != nil {
		timeout = 30 * time.Second
		codedErr := errno.ErrWarningConfigDegraded().
			WithComponent("InfluxDB超时配置").
			WithOperation("解析超时参数").
			WithDetail("处理", "使用30秒默认值")
		log.WarnWithCodef(context.TODO(), codedErr)
	} else {
		timeout = time.Duration(dTmp)
	}

	option := &inner.Option{
		Timeout:              timeout,
		ContentType:          ContentType,
		PerQueryMaxGoroutine: PerQueryMaxGoroutine,
		ChunkSize:            ChunkSize,
		MaxLimit:             MaxLimit,
		MaxSLimit:            MaxSLimit,
		Tolerance:            Tolerance,
	}
	hostList := make(map[string]*inner.Host, len(newData))
	for key, value := range newData {
		hostList[key] = &inner.Host{
			Address:  value.Address,
			Username: value.Username,
			Password: value.Password,
		}
	}
	err = inner.ReloadStorage(s.ctx, hostList, option)
	if err != nil {
		codedErr := errno.ErrStorageConnFailed().
			WithComponent("InfluxDB").
			WithOperation("存储连接").
			WithError(err).
			WithSolution("检查InfluxDB连接配置")
		log.ErrorWithCodef(context.TODO(), codedErr)
		return err
	}
	return nil
}

// loopReloadStorage
func (s *Service) loopReloadStorage(ctx context.Context) error {
	err := s.reloadStorage()
	if err != nil {
		codedErr := errno.ErrConfigReloadFailed().
			WithComponent("InfluxDB").
			WithOperation("循环重载存储").
			WithError(err).
			WithSolution("检查存储重载逻辑")
		log.ErrorWithCodef(context.TODO(), codedErr)
		return err
	}
	ch, err := consul.WatchStorageInfo(ctx)
	if err != nil {
		return err
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for {
			select {
			case <-ctx.Done():
				codedErr := errno.ErrWarningServiceDegraded().
					WithComponent("InfluxDB").
					WithOperation("存储重载循环退出").
					WithDetail("说明", "服务正在关闭")
				log.WarnWithCodef(context.TODO(), codedErr)
				return
			case <-ch:
				log.Debugf(context.TODO(), "get storage info changed notify")
				err = s.reloadStorage()
				if err != nil {
					codedErr := errno.ErrConfigReloadFailed().
						WithComponent("InfluxDB").
						WithOperation("动态重载存储").
						WithError(err).
						WithSolution("检查Consul通知和存储连接")
					log.ErrorWithCodef(context.TODO(), codedErr)
				}
			}
		}
	}()
	return nil
}

// loopReloadTableInfo
func (s *Service) loopReloadTableInfo(ctx context.Context) error {
	err := s.reloadTableInfo()
	if err != nil {
		codedErr := errno.ErrConfigReloadFailed().
			WithComponent("InfluxDB").
			WithOperation("循环重载表信息").
			WithError(err).
			WithSolution("检查表信息重载逻辑")
		log.ErrorWithCodef(context.TODO(), codedErr)
		return err
	}
	ch, err := consul.WatchInfluxdbTableInfo(ctx)
	if err != nil {
		return err
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for {
			select {
			case <-ctx.Done():
				codedErr := errno.ErrWarningServiceDegraded().
					WithComponent("InfluxDB").
					WithOperation("表信息重载循环退出").
					WithDetail("说明", "服务正在关闭")
				log.WarnWithCodef(context.TODO(), codedErr)
				return
			case <-ch:
				log.Debugf(context.TODO(), "get table info changed notify")
				err1 := s.reloadTableInfo()
				if err1 != nil {
					codedErr := errno.ErrConfigReloadFailed().
						WithComponent("InfluxDB").
						WithOperation("动态重载表信息").
						WithError(err1).
						WithSolution("检查Consul表信息通知")
					log.ErrorWithCodef(context.TODO(), codedErr)
				}
			}
		}
	}()
	return nil
}

// reloadInfluxDBRouter 重新加载 InfluxDBRouter
func (s *Service) reloadInfluxDBRouter(ctx context.Context) error {
	dialOpts := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(GrpcMaxCallRecvMsgSize),
			grpc.MaxCallSendMsgSize(GrpcMaxCallSendMsgSize),
		),
	}

	ir := inner.GetInfluxDBRouter()
	err := ir.ReloadRouter(ctx, RouterPrefix, dialOpts)
	if err != nil {
		return err
	}

	s.wg.Add(1)
	go func() {
		ticker := time.NewTicker(PingPeriod)
		defer ticker.Stop()
		defer s.wg.Done()
		for {
			select {
			case <-ctx.Done():
				log.Warnf(ctx, "maintain influxdb host status info loop exit")
				return
			case <-ticker.C:
				ir.Ping(ctx, PingTimeout, PingCount)
				log.Debugf(ctx, "finish to Ping goroutine.")
			}
		}
	}()

	ch := ir.RouterSubscribe(ctx)
	s.wg.Add(1)
	go func() {
		ticker := time.NewTicker(RouterInterval)
		defer ticker.Stop()
		defer s.wg.Done()
		for {
			select {
			case <-ctx.Done():
				log.Warnf(ctx, "space router loop exit")
				return
				// 订阅 redis
			case <-ticker.C:
				err = ir.ReloadAllKey(ctx)
				if err != nil {
					log.Errorf(ctx, err.Error())
				}
				log.Infof(ctx, "ir reload all key time ticker reload")
			case msg := <-ch:
				ir.ReloadByKey(ctx, msg.Payload)
				log.Debugf(ctx, "subscribe msg: %s, space: %s", msg.String(), msg.Payload)
			}
		}
	}()

	return nil
}

// reloadInfluxDBRouter 重新加载 SpaceTsDbRouter
func (s *Service) reloadSpaceTsDbRouter(ctx context.Context) error {
	ir, err := inner.SetSpaceTsDbRouter(ctx, SpaceRouterBboltPath, SpaceRouterBboltBucketName, SpaceRouterPrefix, SpaceRouterBboltWriteBatchSize, IsCache)
	if err != nil {
		return err
	}
	err = ir.ReloadAllKey(ctx, false)
	if err != nil {
		return err
	}

	ch := ir.RouterSubscribe(ctx)
	s.wg.Add(1)
	go func() {
		ticker := time.NewTicker(RouterInterval)
		defer ticker.Stop()
		defer s.wg.Done()
		for {
			select {
			case <-ctx.Done():
				log.Warnf(ctx, "[SpaceTSDB Router] Loop exit")
				return
				// 订阅 redis
			case <-ticker.C:
				err = ir.ReloadAllKey(ctx, true)
				if err != nil {
					log.Errorf(ctx, "[SpaceTSDB Router] TimeTicker reload with error, %v", err)
				}
			case msg := <-ch:
				err = ir.ReloadByChannel(ctx, msg.Channel, msg.Payload)
				if err != nil {
					log.Errorf(ctx, "[SpaceTSDB Router] Subscribe msg with error, %s, %v", msg.String(), err)
				}
			}
		}
	}()
	return nil
}

// reloadStorage: 重载
func (s *Service) reloadRouter() error {
	newData, err := consul.ReloadRouterInfo()
	if err != nil {
		log.Errorf(context.TODO(), "get query router info from consul failed,error:%s", err)
		return err
	}
	hash := consul.HashIt(newData)
	if hash == s.routerHash {
		log.Debugf(context.TODO(), "table hash not changed")
		return err
	}
	inner.ReloadTableInfos(newData)
	s.routerHash = hash
	return nil
}

// reloadMetricRouter: 重载指标路由
func (s *Service) reloadMetricRouter() error {
	newData, err := consul.ReloadMetricInfo()
	if err != nil {
		log.Errorf(context.TODO(), "get query router info from consul failed,error:%s", err)
		return err
	}
	hash := consul.HashIt(newData)
	if hash == s.metricHash {
		log.Debugf(context.TODO(), "metric hash not changed")
		return nil
	}
	inner.ReloadMetricRouter(newData)
	s.metricHash = hash
	return nil
}

// loopReloadRouter: 重载 router
func (s *Service) loopReloadRouter(ctx context.Context) error {
	err := s.reloadRouter()
	if err != nil {
		log.Errorf(context.TODO(), "reload query router failed,error:%s", err)
		return err
	}
	err = s.reloadMetricRouter()
	if err != nil {
		log.Errorf(context.TODO(), "reload metric router failed,error:%s", err)
		return err
	}

	ch, err := consul.WatchQueryRouter(ctx)
	if err != nil {
		return err
	}

	metricCh, err := consul.WatchMetricRouter(ctx)
	if err != nil {
		return err
	}

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for {
			select {
			case <-ctx.Done():
				log.Warnf(context.TODO(), "query router reload loop exit")
				return
			case <-ch:
				log.Debugf(context.TODO(), "get query router info changed notify")
				err1 := s.reloadRouter()
				if err1 != nil {
					log.Errorf(context.TODO(), "reload query router failed,error:%s", err1)
				}
			case <-metricCh:
				log.Debugf(context.TODO(), "get metric router info changed notify")
				err1 := s.reloadMetricRouter()
				if err1 != nil {
					log.Errorf(context.TODO(), "reload metric router failed,error:%s", err1)
				}
			}
		}
	}()
	return nil
}

// reloadBCSInfo: 重载BCSInfo
func (s *Service) reloadBCSInfo() error {
	err := consul.ReloadBCSInfo()
	if err != nil {
		log.Errorf(context.TODO(), "get bcs info info from consul failed,error:%s", err)
		return err
	}

	return err
}

// loopReloadBCSInfo
func (s *Service) loopReloadBCSInfo(ctx context.Context) error {
	err := s.reloadBCSInfo()
	if err != nil {
		log.Errorf(context.TODO(), "reload bcs info failed,error:%s", err)
		return err
	}
	ch, err := consul.WatchBCSInfo(ctx)
	if err != nil {
		return err
	}

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for {
			select {
			case <-ctx.Done():
				log.Warnf(context.TODO(), "bcs info reload loop exit")
				return
			case <-ch:
				log.Debugf(context.TODO(), "get bcs info changed notify")
				err1 := s.reloadBCSInfo()
				if err1 != nil {
					log.Errorf(context.TODO(), "reload bcs info failed,error:%s", err1)
				}
			}
		}
	}()
	return nil
}

// loopReloadDownsampledInfo 重载DownsampledInfo
func (s *Service) loopReloadDownsampledInfo(ctx context.Context) error {
	var err error
	err = consul.LoadDownsampledInfo()
	if err != nil {
		log.Errorf(context.TODO(), "reload downsampled info failed,error:%s", err)
		return err
	}
	ch, err := consul.WatchDownsampledInfo(ctx)
	if err != nil {
		return err
	}

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for {
			select {
			case <-ctx.Done():
				log.Warnf(context.TODO(), "downsampled info reload loop exit")
				return
			case <-ch:
				log.Debugf(context.TODO(), "get downsampled info changed notify")
				err = consul.LoadDownsampledInfo()
				if err != nil {
					log.Errorf(context.TODO(), "reload downsampled info failed, err: %s", err)
				}
			}
		}
	}()
	return nil
}
