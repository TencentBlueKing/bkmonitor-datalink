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
	"strings"
	"time"

	"github.com/jinzhu/gorm"
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/elasticsearch"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/cipher"
)

//go:generate goqueryset -in clusterinfo.go -out qs_clusterinfo_gen.go

var IgnoredStorageClusterTypes = []string{"victoria_metrics"} // 忽略的结果表类型,vm类型结果表不写入consul

// Event: cluster info model
// gen:qs
type ClusterInfo struct {
	ClusterID                 uint      `gorm:"primary_key" json:"cluster_id"`
	ClusterName               string    `gorm:"size:128;unique" json:"cluster_name"`
	DisplayName               string    `gorm:"size:128" json:"display_name"`
	ClusterType               string    `gorm:"size:32;index" json:"cluster_type"`
	DomainName                string    `gorm:"size:128" json:"domain_name"`
	Port                      uint      `json:"port"`
	Description               string    `gorm:"size:256" json:"description"`
	IsDefaultCluster          bool      `json:"is_default_cluster"`
	Password                  string    `gorm:"size:128" json:"password"`
	Username                  string    `gorm:"size:64" json:"username"`
	IsSslVerify               bool      `json:"is_ssl_verify"`
	Schema                    *string   `gorm:"size:32" json:"schema"`
	Version                   *string   `gorm:"size:64" json:"version"`
	RegisteredSystem          string    `gorm:"size:128" json:"registered_system"`
	CustomOption              string    `json:"custom_option"`
	CreateTime                time.Time `json:"create_time"`
	Creator                   string    `gorm:"size:255" json:"creator"`
	LastModifyTime            time.Time `gorm:"last_modify_time" json:"last_modify_time"`
	LastModifyUser            string    `gorm:"size:32" json:"last_modify_user"`
	GseStreamToId             int       `gorm:"column:gse_stream_to_id" json:"gse_stream_to_id"`
	IsRegisterToGse           bool      `gorm:"column:is_register_to_gse" json:"is_register_to_gse"`
	DefaultSettings           string    `gorm:"default_settings" json:"default_settings"`
	Label                     string    `gorm:"size:32" json:"label"`
	SslCertificate            string    `json:"ssl_certificate"`
	SslCertificateAuthorities string    `json:"ssl_certificate_authorities"`
	SslCertificateKey         string    `json:"ssl_certificate_key"`
	SslInsecureSkipVerify     bool      `gorm:"column:ssl_insecure_skip_verify" json:"ssl_insecure_skip_verify"`
	SslVerificationMode       string    `gorm:"size:16" json:"ssl_verification_mode"`
	ExtranetDomainName        string    `gorm:"size:128" json:"extranet_domain_name"`
	ExtranetPort              uint      `gorm:"column:extranet_port" json:"extranet_port"`
	// 是否开启鉴权
	IsAuth bool `gorm:"column:is_auth" json:"is_auth"`
}

// TableName: 用于设置表的别名
func (ClusterInfo) TableName() string {
	return "metadata_clusterinfo"
}

func (c ClusterInfo) GetESClient(ctx context.Context) (*elasticsearch.Elasticsearch, error) {
	if c.ClusterType != models.StorageTypeES {
		return nil, errors.Errorf("record type error")
	}
	// 获取ES版本，创建ES客户端
	var version, schema string
	if c.Version != nil {
		version = *c.Version
	} else {
		version = "7"
	}
	if c.Schema != nil {
		schema = *c.Schema
	} else {
		schema = "http"
	}
	esVersion := strings.Split(version, ".")[0]
	address := elasticsearch.ComposeESHosts(schema, c.DomainName, c.Port)
	// 密码解密
	password, err := cipher.GetDBAESCipher().AESDecrypt(c.Password)
	if err != nil {
		return nil, err
	}
	client, err := elasticsearch.NewElasticsearch(esVersion, address, c.Username, password)
	if err != nil {
		return nil, err
	}
	timeoutCtx, _ := context.WithTimeout(ctx, 5*time.Second)
	resp, err := client.Ping(timeoutCtx)
	// ref: https://andrii-kushch.medium.com/is-it-necessary-to-close-the-body-in-the-http-response-object-in-golang-171c44c9394d
	if err != nil {
		return nil, err
	}
	defer resp.Close()

	return client, nil
}

// BeforeCreate 新建前时间字段设置为当前时间
func (c *ClusterInfo) BeforeCreate(tx *gorm.DB) error {
	c.CreateTime = time.Now()
	c.LastModifyTime = time.Now()
	return nil
}

// BeforeUpdate 保存前最后修改时间字段设置为当前时间
func (c *ClusterInfo) BeforeUpdate(tx *gorm.DB) error {
	c.LastModifyTime = time.Now()
	return nil
}
