// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package processor

type Config []ProcessorConfig

type ProcessorConfig struct {
	Name   string                 `config:"name"`
	Config map[string]interface{} `config:"config"`
}

type ProcessorsIDConfig struct {
	ID        string            `config:"id"`
	Processor []ProcessorConfig `config:"processor"`
}

type SubConfigDefault struct {
	Processor []ProcessorConfig `config:"processor"`
}

const (
	KeyInstance = "bk.instance.id"
	KeyService  = "service.name"
	KeyKind     = "kind"
)

type SubConfig struct {
	Type     string               `config:"type"`
	Token    string               `config:"token"`
	Default  SubConfigDefault     `config:"default"`
	Service  []ProcessorsIDConfig `config:"service"`
	Instance []ProcessorsIDConfig `config:"instance"`
}

type SubConfigProcessor struct {
	Token  string
	Type   string
	ID     string
	Config ProcessorConfig
}
