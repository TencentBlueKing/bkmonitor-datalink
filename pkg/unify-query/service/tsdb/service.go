// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package tsdb

import (
	"context"
	"sync"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	inner "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb"
)

type Service struct {
	ctx        context.Context
	cancelFunc context.CancelFunc
	wg         *sync.WaitGroup

	storageHash string
}

// Type
func (s *Service) Type() string {
	return "prometheus"
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

	log.Debugf(context.TODO(), "waiting for prometheus service close")
	// 等待上一次服务结束
	s.Wait()

	// 更新上下文控制方法
	s.ctx, s.cancelFunc = context.WithCancel(ctx)
	log.Debugf(context.TODO(), "prometheus service context update success.")

	err = s.loopReloadStorage(s.ctx)
	if err != nil {
		log.Errorf(context.TODO(), "prometheus service close success")
		return
	}

	log.Infof(ctx, "prometheus service start success.")
}

// Wait
func (s *Service) Wait() {
	s.wg.Wait()
}

// Close
func (s *Service) Close() {
	s.cancelFunc()
	log.Infof(context.TODO(), "prometheus service close success")
}

// loopReloadStorage
func (s *Service) loopReloadStorage(ctx context.Context) error {
	err := s.reloadStorage()
	if err != nil {
		log.Errorf(context.TODO(), "reload storage failed")
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
				log.Warnf(context.TODO(), "prometheus service close success")
				return
			case <-ch:
				log.Debugf(context.TODO(), "get storage info changed notify")
				err = s.reloadStorage()
				if err != nil {
					log.Errorf(context.TODO(), "reload storage failed %v", err)
				}
			}
		}
	}()
	return nil
}

// reloadStorage 加载 storage 实例
func (s *Service) reloadStorage() error {
	consulData, err := consul.GetTsDBStorageInfo()
	if err != nil {
		log.Errorf(context.TODO(), "get storage info failed %v", err)
		return err
	}
	hash := consul.HashIt(consulData)
	if hash == s.storageHash {
		log.Debugf(context.TODO(), "storage hash not changed")
		return err
	}

	options := &inner.Options{
		InfluxDB: &inner.InfluxDBOption{
			Timeout:        InfluxDBTimeout,
			ContentType:    InfluxDBContentType,
			ChunkSize:      InfluxDBChunkSize,
			RawUriPath:     InfluxDBQueryRawUriPath,
			Accept:         InfluxDBQueryRawAccept,
			AcceptEncoding: InfluxDBQueryRawAcceptEncoding,
			MaxLimit:       InfluxDBMaxLimit,
			MaxSLimit:      InfluxDBMaxSLimit,
			Tolerance:      InfluxDBTolerance,
			RouterPrefix:   InfluxDBRouterPrefix,
			ReadRateLimit:  InfluxDBQueryReadRateLimit,
		},
		Es: &inner.ESOption{
			Timeout:    EsTimeout,
			MaxRouting: EsMaxRouting,
			MaxSize:    EsMaxSize,
		},
	}
	err = inner.ReloadTsDBStorage(s.ctx, consulData, options)
	if err != nil {
		log.Errorf(context.TODO(), "reload storage failed %v", err)
		return err
	}

	// 成功时记录本次 hash，避免下一次 reload 因 hash 未更新而长期短路、内存与配置源不一致
	s.storageHash = hash
	ids := make([]string, 0, len(consulData))
	for k := range consulData {
		ids = append(ids, k)
	}
	// 便于核对当前进程已加载的 storage_id 全集（排查「路由有 id、内存无 id」）
	log.Infof(s.ctx, "[storage-reload] success: count=%d ids=%v", len(ids), ids)
	return nil
}
