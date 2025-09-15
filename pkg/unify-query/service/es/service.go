// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package es

import (
	"context"
	"sync"
	"time"

	"github.com/spf13/viper"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/es"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
)

// Service 服务侧初始化
type Service struct {
	ctx        context.Context
	cancelFunc context.CancelFunc
	wg         *sync.WaitGroup

	storageHash string
	tableHash   string
}

// Type
func (s *Service) Type() string {
	return "es"
}

// Start
func (s *Service) Start(ctx context.Context) {
	s.Reload(ctx)
}

// reloadStorage
func (s *Service) reloadStorage() error {
	newData, err := consul.GetESStorageInfo()
	if err != nil {
		log.Errorf(context.TODO(), "get storage info from consul failed,error:%s", err)
		return err
	}
	hash := consul.HashIt(newData)
	if hash == s.storageHash {
		log.Debugf(context.TODO(), "storage hash not changed")
		return err
	}
	infos := make(map[string]*es.ESInfo)
	for key, value := range newData {
		infos[key] = &es.ESInfo{
			Host:           value.Address,
			Username:       value.Username,
			Password:       value.Password,
			MaxConcurrency: viper.GetInt(MaxConcurrencyConfigPath),
		}
	}
	err = es.ReloadStorage(infos)
	if err != nil {
		log.Errorf(context.TODO(), "reload storage failed,error:%s", err)
		return err
	}
	return nil
}

// loopReloadStorage
func (s *Service) loopReloadStorage(ctx context.Context) error {
	err := s.reloadStorage()
	if err != nil {
		log.Errorf(context.TODO(), "reload storage failed,error:%s", err)
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
				log.Warnf(context.TODO(), "storage reload loop exit")
				return
			case <-ch:
				log.Debugf(context.TODO(), "get storage info changed notify")
				err = s.reloadStorage()
				if err != nil {
					log.Errorf(context.TODO(), "reload storage failed,error:%s", err)
				}

			}
		}
	}()
	return nil
}

// reloadTableInfo
func (s *Service) reloadTableInfo() error {
	newData, err := consul.GetESTableInfo()
	if err != nil {
		log.Errorf(context.TODO(), "get data from consul failed,error:%s", err)
		return err
	}
	hash := consul.HashIt(newData)
	if hash == s.tableHash {
		log.Debugf(context.TODO(), "table hash not changed")
		return err
	}
	infos := make(map[string]*es.TableInfo)
	for key, value := range newData {
		infos[key] = &es.TableInfo{
			StorageID:   value.StorageID,
			AliasFormat: value.AliasFormat,
			DateFormat:  value.DateFormat,
			DateStep:    value.DateStep,
		}
	}
	err = es.ReloadTableInfo(infos)
	if err != nil {
		log.Errorf(context.TODO(), "reload table info failed,error:%s", err)
		return err
	}
	return nil
}

// loopReloadTableInfo
func (s *Service) loopReloadTableInfo(ctx context.Context) error {
	err := s.reloadTableInfo()
	if err != nil {
		log.Errorf(context.TODO(), "reload table info failed,error:%s", err)
		return err
	}
	ch, err := consul.WatchESTableInfo(ctx)
	if err != nil {
		return err
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for {
			select {
			case <-ctx.Done():
				log.Warnf(context.TODO(), "table reload loop exit")
				return
			case <-ch:
				log.Debugf(context.TODO(), "get table info changed notify")
				err1 := s.reloadTableInfo()
				if err1 != nil {
					log.Errorf(context.TODO(), "reload table info failed,error:%s", err1)
				}
			}
		}
	}()
	return nil
}

// loopRefreshAliasInfo
func (s *Service) loopRefreshAliasInfo(ctx context.Context) error {
	es.RefreshAllAlias()
	duration := AliasRefreshPeriod

	ticker := time.NewTicker(duration)
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				log.Warnf(context.TODO(), "alias refresh exit")
				return
			case <-ticker.C:
				log.Debugf(context.TODO(), "ticker alarm,start refresh alias")
				es.RefreshAllAlias()
				log.Debugf(context.TODO(), "refresh alias done")
			}
		}
	}()
	return nil
}

// Reload
func (s *Service) Reload(ctx context.Context) {
	if s.wg == nil {
		s.wg = new(sync.WaitGroup)
	}
	// 关闭上一次的consul instance
	if s.cancelFunc != nil {
		s.cancelFunc()
	}

	log.Debugf(context.TODO(), "waiting for es service close")
	// 等待服务结束
	s.Wait()

	// 更新上下文控制方法
	s.ctx, s.cancelFunc = context.WithCancel(ctx)
	log.Debugf(context.TODO(), "es service context update success.")
	err := s.loopReloadStorage(s.ctx)
	if err != nil {
		log.Errorf(context.TODO(), "start loop reload es storage failed for->[%s]", err)
		return
	}
	err = s.loopReloadTableInfo(s.ctx)
	if err != nil {
		log.Errorf(context.TODO(), "start loop reload es table info failed for->[%s]", err)
		return
	}
	err = s.loopRefreshAliasInfo(s.ctx)
	if err != nil {
		log.Errorf(context.TODO(), "start loop refresh es alias info failed for->[%s]", err)
		return
	}
	log.Warnf(context.TODO(), "es service reloaded or start success.")
}

// Wait
func (s *Service) Wait() {
	s.wg.Wait()
}

// Close
func (s *Service) Close() {
	s.cancelFunc()
	log.Infof(context.TODO(), "es service context cancel func called.")
}
