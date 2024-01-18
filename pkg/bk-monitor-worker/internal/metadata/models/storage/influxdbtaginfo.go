// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package storage

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/pkg/errors"

	cfg "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/hashconsul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

//go:generate goqueryset -in influxdbtaginfo.go -out qs_influxdbtaginfo_gen.go

// InfluxdbTagInfo influxdb tag info model
// gen:qs
type InfluxdbTagInfo struct {
	Database             string `gorm:"size:128" json:"database"`
	Measurement          string `gorm:"size:128" json:"measurement"`
	TagName              string `gorm:"size:128" json:"tag_name"`
	TagValue             string `gorm:"size:128" json:"tag_value"`
	ClusterName          string `gorm:"size:128" json:"cluster_name"`
	HostList             string `gorm:"size:128" json:"host_list"`
	ManualUnreadableHost string `gorm:"size:128" json:"manual_unreadable_host"`
	ForceOverwrite       bool   `gorm:"column:force_overwrite" json:"force_overwrite"`
}

// TableName 用于设置表的别名
func (InfluxdbTagInfo) TableName() string {
	return "metadata_influxdbtaginfo"
}

func (i InfluxdbTagInfo) GenerateTagKey() string {
	return fmt.Sprintf("%s/%s/%s==%s", i.Database, i.Measurement, i.TagName, i.TagValue)
}

func (InfluxdbTagInfo) ConsulPath() string {
	return fmt.Sprintf(models.InfluxdbTagInfoConsulPathTemplate, cfg.StorageConsulPathPrefix, cfg.BypassSuffixPath)
}

func (i InfluxdbTagInfo) RedisField() string {
	return fmt.Sprintf("%s/%s", i.ClusterName, i.GenerateTagKey())
}

func (i InfluxdbTagInfo) ConsulConfigPath() string {
	return fmt.Sprintf("%s/%s/%s", i.ConsulPath(), i.ClusterName, i.GenerateTagKey())
}

func (i InfluxdbTagInfo) GenerateNewInfo(oldInfo TagItemInfo) (TagItemInfo, error) {
	var deleteList = make([]string, 0)
	var addList = make([]string, 0)
	var oldHostList = oldInfo.HostList
	var newHostList = strings.Split(i.HostList, ",")
	// 获取需要删除的主机列表
	for _, oldHost := range oldHostList {
		exist := false
		for _, newHost := range newHostList {
			if newHost == oldHost {
				exist = true
				break
			}
		}
		if !exist {
			deleteList = append(deleteList, oldHost)
		}
	}
	// 获取需要增加的主机列表
	for _, newHost := range newHostList {
		exist := false
		for _, oldHost := range oldHostList {
			if newHost == oldHost {
				exist = true
				break
			}
		}
		if !exist {
			addList = append(addList, newHost)
		}
	}
	if len(addList) == 0 && len(deleteList) == 0 {
		return oldInfo, nil
	}
	// 使用中的主机列表不动，进行预新增和预删除，该info会被transport继续处理
	newInfo := TagItemInfo{
		HostList:          oldHostList,
		UnreadableHost:    addList,
		DeleteHostList:    deleteList,
		Status:            "changed",
		TransportStartAt:  0,
		TransportLastAt:   0,
		TransportFinishAt: 0,
	}
	return newInfo, nil
}

// AddConsulInfo 新增consul信息
func (i InfluxdbTagInfo) AddConsulInfo(ctx context.Context) error {
	var unreadble []string
	if i.ManualUnreadableHost != "" {
		unreadble = strings.Split(i.ManualUnreadableHost, ",")
	} else {
		unreadble = make([]string, 0)
	}
	var hostListObj []string
	if i.HostList == "" {
		hostListObj = make([]string, 0)
	} else {
		hostListObj = strings.Split(i.HostList, ",")
	}
	info := TagItemInfo{
		HostList:       hostListObj,
		UnreadableHost: unreadble,
		DeleteHostList: make([]string, 0),
		Status:         "ready",
	}

	consulClient, err := consul.GetInstance()
	if err != nil {
		return err
	}
	val, err := jsonx.MarshalString(info)
	if err != nil {
		return err
	}
	err = hashconsul.Put(consulClient, i.ConsulConfigPath(), val)
	if err != nil {
		return err
	}
	return nil
}

// GetConsulInfo 从consul中获取信息
func (i InfluxdbTagInfo) GetConsulInfo(ctx context.Context) (*TagItemInfo, error) {
	consulClient, err := consul.GetInstance()
	if err != nil {
		return nil, err
	}
	dataBytes, err := consulClient.Get(i.ConsulConfigPath())
	if err != nil {
		return nil, err
	}
	var data TagItemInfo
	err = jsonx.Unmarshal(dataBytes, &data)
	if err != nil {
		return nil, err
	}
	return &data, nil
}

// ModifyConsulInfo 更新consul信息
func (i InfluxdbTagInfo) ModifyConsulInfo(ctx context.Context, oldInfo TagItemInfo) error {
	// 如果状态不为ready，则不应修改
	if oldInfo.Status != "ready" {
		return nil
	}

	newInfo, err := i.GenerateNewInfo(oldInfo)
	if err != nil {
		return err
	}
	val, err := jsonx.MarshalString(newInfo)
	if err != nil {
		return err
	}
	consulClient, err := consul.GetInstance()
	if err != nil {
		return err
	}
	err = hashconsul.Put(consulClient, i.ConsulConfigPath(), val)

	models.PushToRedis(ctx, models.InfluxdbTagInfoKey, i.RedisField(), val, true)
	return nil
}

// RefreshConsulConfig 更新tag路由信息
func (i InfluxdbTagInfo) RefreshConsulConfig(ctx context.Context) error {
	// 强制刷新模式下，直接刷新对应tag的数据即可
	if i.ForceOverwrite {
		err := i.AddConsulInfo(ctx)
		if err != nil {
			return err
		}
		return nil
	}
	// 根据item信息,到consul中获取数据
	config, err := i.GetConsulInfo(ctx)
	if err != nil {
		if errors.Is(consul.NotFoundErr, err) {
			// 没有数据直接刷新对应tag的数据
			err := i.AddConsulInfo(ctx)
			if err != nil {
				return err
			}
			return nil
		} else {
			return err
		}
	}
	// 否则进行更新
	err = i.ModifyConsulInfo(ctx, *config)
	if err != nil {
		return err
	}
	return nil
}

// RefreshConsulTagConfig 更新tag路由信息
func RefreshConsulTagConfig(ctx context.Context, objs *[]InfluxdbTagInfo, goroutineLimit int) {
	var clusterNameList []string
	wg := &sync.WaitGroup{}
	ch := make(chan bool, goroutineLimit)
	wg.Add(len(*objs))
	for _, tagInfo := range *objs {
		ch <- true
		clusterNameList = append(clusterNameList, tagInfo.ClusterName)
		go func(tagInfo InfluxdbTagInfo, wg *sync.WaitGroup, ch chan bool) {
			defer func() {
				<-ch
				wg.Done()
			}()
			err := tagInfo.RefreshConsulConfig(ctx)
			if err != nil {
				logger.Errorf("db [%s] tag [%s] try to refresh consul tag config, %v", tagInfo.Database, tagInfo.TagName, err)
			} else {
				logger.Infof("db [%s] tag [%s] refresh consul tag config success", tagInfo.Database, tagInfo.TagName)
			}
		}(tagInfo, wg, ch)
	}
	wg.Wait()
	tagConsulPath := InfluxdbTagInfo{}.ConsulPath()
	for _, clusterName := range clusterNameList {
		err := models.RefreshRouterVersion(ctx, fmt.Sprintf("%s/%s/version/", tagConsulPath, clusterName))
		if err != nil {
			logger.Errorf("cluster [%s] update tag_info version failed, %v", clusterName, err)
		}
	}

}

type TagItemInfo struct {
	HostList          []string `json:"host_list"`
	UnreadableHost    []string `json:"unreadable_host"`
	DeleteHostList    []string `json:"delete_host_list"`
	Status            string   `json:"status"`
	TransportStartAt  int      `json:"transport_start_at"`
	TransportLastAt   int      `json:"transport_last_at"`
	TransportFinishAt int      `json:"transport_finish_at"`
}
