// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package common

// Points :
type Points []Point

// Point 数据载体
type Point struct {
	DB          string
	Measurement string
	Tags        Tags
	Start       int
	End         int
}

// Tag represents a single key/value tag pair.
type Tag struct {
	Key   []byte
	Value []byte
}
type Tags []Tag
