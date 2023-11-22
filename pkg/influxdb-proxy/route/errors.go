// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package route

import (
	"errors"
)

// :
var (
	ErrGetClusterFailed = errors.New("get cluster failed")
	ErrMissingDB        = errors.New("missing db")
	ErrMissingTable     = errors.New("missing table")
	ErrBackupIsNil      = errors.New("backup is nil")
	ErrWrongFormatRoute = errors.New("wrong route format")
)

// :
var (
	ErrTableNameMatchFailed      = errors.New("table name match failed")
	ErrMatchClusterByRouteFailed = errors.New("match cluster by route failed")
)

// :
var (
	ErrClusterWriteFailed    = errors.New("write cluster failed")
	ErrClusterQueryFailed    = errors.New("query cluster failed")
	ErrClusterCreateDBFailed = errors.New("createDB cluster failed")
)

// :
var (
	ErrServerNotReady  = errors.New("server not ready")
	ErrAuthFailed      = errors.New("authorization failed")
	ErrMethodNotMatch  = errors.New("method not match")
	ErrSQLMatchFailed  = errors.New("sql match by regexp failed")
	ErrSQLNotSupported = errors.New("not supported sql")
	ErrEmptyData       = errors.New("empty data")

	ErrGzipReadFailed = errors.New("gzip read failed")
	ErrReadBodyFailed = errors.New("read body failed")
)
