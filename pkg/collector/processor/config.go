// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package processor

type Configs []Config

type Config struct {
	Name   string         `config:"name"`
	Config map[string]any `config:"config"`
}

type IDConfig struct {
	ID        string   `config:"id"`
	Processor []Config `config:"processor"`
}

type SubConfig struct {
	Type     string           `config:"type"`
	Token    string           `config:"token"`
	Default  SubConfigDefault `config:"default"`
	Service  []IDConfig       `config:"service"`
	Instance []IDConfig       `config:"instance"`
}

type SubConfigDefault struct {
	Processor []Config `config:"processor"`
}

type SubConfigProcessor struct {
	Token  string
	Type   string
	ID     string
	Config Config
}
