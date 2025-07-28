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
	"fmt"
	"strings"
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

func (i InfluxdbTagInfo) RedisField() string {
	return fmt.Sprintf("%s/%s", i.ClusterName, i.GenerateTagKey())
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

type TagItemInfo struct {
	HostList          []string `json:"host_list"`
	UnreadableHost    []string `json:"unreadable_host"`
	DeleteHostList    []string `json:"delete_host_list"`
	Status            string   `json:"status"`
	TransportStartAt  int      `json:"transport_start_at"`
	TransportLastAt   int      `json:"transport_last_at"`
	TransportFinishAt int      `json:"transport_finish_at"`
}
