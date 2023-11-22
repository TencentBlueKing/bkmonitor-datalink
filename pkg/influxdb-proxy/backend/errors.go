// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package backend

import (
	"github.com/pkg/errors"
)

// :
var (
	ErrPartitionNotExists     = errors.New("partition not exists")
	ErrQueryError             = errors.New("query database failed")
	ErrGetHostInfoFailed      = errors.New("get hostinfo failed ")
	ErrBackendIsNil           = errors.New("backend is nil")
	ErrClosed                 = errors.New("backend is closed")
	ErrURLParamsConvertFailed = errors.New("fail to convert URL params")
	// 从backend返回的非200错误不会以error体现，而是response的errorcode，所以需要特殊处理
	// ErrNot200 = errors.New("get not 200 code after operation to influxdb")
)

// :
var (
	ErrReadBody           = errors.New("read body failed")
	ErrGetAuth            = errors.New("get auth failed")
	ErrInitKafkaBackup    = errors.New("init kafka for backup failed")
	ErrGetHostInfo        = errors.New("get hostinfo failed ")
	ErrGetDomainName      = errors.New("get domain name failed ")
	ErrGetPort            = errors.New("get port failed ")
	ErrBackupDataToBuffer = errors.New("backup data to buffer failed ")
	ErrPushDataToKafka    = errors.New("push data to kafka failed ")
	ErrInitRequest        = errors.New("init request failed")
	ErrReadReader         = errors.New("read reader failed")
	ErrBackupData         = errors.New("backup data failed")
	ErrNetwork            = errors.New("network error")
	ErrWriteBackup        = errors.New("backup after write failed")
	ErrDoQuery            = errors.New("do query failed,not network error")
	ErrDoWrite            = errors.New("do write failed")
	ErrDoPing             = errors.New("do ping failed")
	ErrWrongPing          = errors.New("get wrong ping response")
	ErrLowerZeroOffset    = errors.New("offset lower than zero")
)

// :
var (
	ErrBackendNotExist       = errors.New("backend not exist")
	ErrBackendNotExistInList = errors.New("some backend not exist when try to get backendlist")
	ErrGetDomainFailed       = errors.New("get domain failed ")
	ErrGetPortFailed         = errors.New("get port failed ")
	ErrRefreshFailed         = errors.New("get errors when refreshing")
	ErrBackupIsNil           = errors.New("backup is nil")
)
