// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package sidecar

type Config struct {
	ConfigPath    string       `yaml:"config_path"`
	PidPath       string       `yaml:"pid_path"`
	KubConfig     string       `yaml:"kubeconfig"`
	ApiServerHost string       `yaml:"apiserver_host"`
	TLS           TLSConfig    `yaml:"tls"`
	Secret        SecretConfig `yaml:"secret"`
}

type TLSConfig struct {
	Insecure bool   `yaml:"insecure"`
	CertFile string `yaml:"cert_file"`
	KeyFile  string `yaml:"key_file"`
	CAFile   string `yaml:"ca_file"`
}

type SecretConfig struct {
	Namespace string `yaml:"namespace"`
	Selector  string `yaml:"selector"`
}
