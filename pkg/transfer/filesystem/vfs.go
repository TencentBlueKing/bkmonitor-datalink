// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package filesystem

import (
	"github.com/spf13/afero"
)

// FileSystem is the filesystem interface.
//
// Any simulated or real filesystem should implement this interface.
type FileSystem = afero.Fs

// File represents a file in the filesystem.
type File = afero.File

// FS :
var FS FileSystem

func init() {
	FS = afero.NewOsFs()
}
