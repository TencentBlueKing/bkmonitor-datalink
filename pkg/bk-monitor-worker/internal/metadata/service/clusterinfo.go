// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package service

import (
	"encoding/base64"
	"fmt"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/storage"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/cipher"
)

// ClusterInfoSvc cluster info service
type ClusterInfoSvc struct {
	*storage.ClusterInfo
}

func NewClusterInfoSvc(obj *storage.ClusterInfo) ClusterInfoSvc {
	return ClusterInfoSvc{
		ClusterInfo: obj,
	}
}

// ConsulConfig 获取集群的consul配置信息
func (k ClusterInfoSvc) ConsulConfig() ClusterInfoConsulConfig {
	return ClusterInfoConsulConfig{
		ClusterConfig: ClusterConfig{
			DomainName:                   k.DomainName,
			Port:                         k.Port,
			ExtranetDomainName:           k.ExtranetDomainName,
			ExtranetPort:                 k.ExtranetPort,
			Schema:                       k.Schema,
			IsSslVerify:                  k.IsSslVerify,
			SslVerificationMode:          k.SslVerificationMode,
			SslInsecureSkipVerify:        k.SslInsecureSkipVerify,
			SslCertificateAuthorities:    k.base64WithPrefix(k.SslCertificateAuthorities),
			SslCertificate:               k.base64WithPrefix(k.SslCertificate),
			SslCertificateKey:            k.base64WithPrefix(k.SslCertificateKey),
			RawSslCertificateAuthorities: k.SslCertificateAuthorities,
			RawSslCertificate:            k.SslCertificate,
			RawSslCertificateKey:         k.SslCertificateKey,
			ClusterId:                    k.ClusterID,
			ClusterName:                  k.ClusterName,
			Version:                      k.Version,
			CustomOption:                 k.CustomOption,
			RegisteredSystem:             k.RegisteredSystem,
			Creator:                      k.Creator,
			CreateTime:                   k.CreateTime.Unix(),
			LastModifyUser:               k.LastModifyUser,
			IsDefaultCluster:             k.IsDefaultCluster,
		},
		ClusterType: k.ClusterType,
		AuthInfo: AuthInfo{
			Password: cipher.AESDecrypt(k.Password),
			Username: k.Username,
		},
	}
}

// base64WithPrefix 编码，并添加上前缀
func (k ClusterInfoSvc) base64WithPrefix(content string) string {
	if content == "" {
		return content
	}
	prefix := "base64://"
	encoded := base64.StdEncoding.EncodeToString([]byte(content))
	encodedWithPrefix := fmt.Sprintf("%s%s", prefix, encoded)
	return encodedWithPrefix
}

// ClusterInfoConsulConfig 集群的consul配置结构
type ClusterInfoConsulConfig struct {
	ClusterConfig ClusterConfig `json:"cluster_config"`
	ClusterType   string        `json:"cluster_type"`
	AuthInfo      AuthInfo      `json:"auth_info"`
}

// AuthInfo 集群登陆信息
type AuthInfo struct {
	Password string `json:"password"`
	Username string `json:"username"`
}

// ClusterConfig 集群配置信息
type ClusterConfig struct {
	DomainName                   string `json:"domain_name"`
	Port                         uint   `json:"port"`
	ExtranetDomainName           string `json:"extranet_domain_name"`
	ExtranetPort                 uint   `json:"extranet_port"`
	Schema                       string `json:"schema"`
	IsSslVerify                  bool   `json:"is_ssl_verify"`
	SslVerificationMode          string `json:"ssl_verification_mode"`
	SslInsecureSkipVerify        bool   `json:"ssl_insecure_skip_verify"`
	SslCertificateAuthorities    string `json:"ssl_certificate_authorities"`
	SslCertificate               string `json:"ssl_certificate"`
	SslCertificateKey            string `json:"ssl_certificate_key"`
	RawSslCertificateAuthorities string `json:"raw_ssl_certificate_authorities"`
	RawSslCertificate            string `json:"raw_ssl_certificate"`
	RawSslCertificateKey         string `json:"raw_ssl_certificate_key"`
	ClusterId                    uint   `json:"cluster_id"`
	ClusterName                  string `json:"cluster_name"`
	Version                      string `json:"version"`
	CustomOption                 string `json:"custom_option"`
	RegisteredSystem             string `json:"registered_system"`
	Creator                      string `json:"creator"`
	CreateTime                   int64  `json:"create_time"`
	LastModifyUser               string `json:"last_modify_user"`
	IsDefaultCluster             bool   `json:"is_default_cluster"`
}
