// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package consul

const (
	ServiceNameConfigPath = "consul.service_name"
	KVBasePathConfigPath  = "consul.kv_base_path"
	AddressConfigPath     = "consul.consul_address"
	TLSCaFileConfigPath   = "consul.tls.ca_file_path"
	TLSKeyFileConfigPath  = "consul.tls.key_file_path"
	TLSCertFileConfigPath = "consul.tls.cert_file_path"
	TLSSkipVerify         = "consul.tls.skip_verify"
	HTTPAddressConfigPath = "http.address"
	PortConfigPath        = "http.port"
	TTLConfigPath         = "consul.check_ttl"
)

var (
	ServiceName string
	KVBasePath  string

	HTTPAddress string
	Port        int
	TTL         string

	Address       string
	CaFilePath    string
	KeyFilePath   string
	CertFilePath  string
	SkipTLSVerify bool
)
