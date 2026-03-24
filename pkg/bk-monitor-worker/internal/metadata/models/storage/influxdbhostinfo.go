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
	"strings"
	"time"

	"github.com/jinzhu/gorm"
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/cipher"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/timex"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

//go:generate goqueryset -in influxdbhostinfo.go -out qs_influxdbhostinfo_gen.go

// InfluxdbHostInfo influxdb host info model
// gen:qs
type InfluxdbHostInfo struct {
	HostName        string  `gorm:"primary_key;size:128" json:"host_name"`
	DomainName      string  `gorm:"size:128" json:"domain_name"`
	Port            uint    `json:"port"`
	Username        string  `gorm:"size:64" json:"username"`
	Password        string  `gorm:"password" json:"password"`
	Description     string  `gorm:"size:256" json:"description"`
	Status          bool    `gorm:"column:status" json:"status"`
	GrpcPort        uint    `gorm:"column:grpc_port" json:"grpc_port"`
	BackupRateLimit float64 `gorm:"column:backup_rate_limit" json:"backup_rate_limit"`
	ReadRateLimit   float64 `gorm:"column:read_rate_limit" json:"read_rate_limit"`
	Protocol        string  `gorm:"column:protocol" json:"protocol"`
}

// TableName 用于设置表的别名
func (InfluxdbHostInfo) TableName() string {
	return "metadata_influxdbhostinfo"
}

// BeforeCreate 配置默认字段
func (i *InfluxdbHostInfo) BeforeCreate(tx *gorm.DB) error {
	if i.Protocol == "" {
		i.Protocol = "http"
	}
	if i.GrpcPort == 0 {
		i.GrpcPort = 8090
	}
	return nil
}

// GetConsulConfig 生成consul配置信息
func (i InfluxdbHostInfo) GetConsulConfig() map[string]any {
	pwd, err := cipher.GetDBAESCipher().AESDecrypt(i.Password)
	if err != nil {
		logger.Error("GetConsulConfig:get influxdb host info password error", err)
		return nil
	}
	return map[string]any{
		"domain_name":       i.DomainName,
		"port":              i.Port,
		"username":          i.Username,
		"password":          pwd,
		"status":            i.Status,
		"backup_rate_limit": i.BackupRateLimit,
		"grpc_port":         i.GrpcPort,
		"protocol":          i.Protocol,
		"read_rate_limit":   i.ReadRateLimit,
	}
}

// JudgeShardByDuration 用于根据数据保留时间判断shard的长度
func JudgeShardByDuration(duration string) (string, error) {
	// 当输入为inf时，可以忽略大小写，并表示无限保留，此时的shard为7d
	if duration == "" || strings.ToLower(duration) == "inf" {
		return "7d", nil
	}

	durationValue, err := timex.ParseDuration(duration)
	if err != nil {
		return "", err
	}
	// duration必须大于1h
	if durationValue < time.Hour {
		return "", errors.New("duration must gte 1h")
	} else if durationValue < time.Hour*48 {
		// 小于2d时，shard为1h
		return "1h", nil
	} else if durationValue <= time.Hour*180*24 {
		// duration大于2d小于180d时，shard为1d
		return "1d", nil
	} else {
		// duration大于180d时，shard为7d
		return "7d", nil
	}
}
