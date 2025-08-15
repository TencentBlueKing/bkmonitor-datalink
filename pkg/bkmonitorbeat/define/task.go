// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package define

import (
	"context"
)

// 所有模块的module名引用
const (
	ModuleGlobalHeartBeat = "global_heartbeat"
	ModuleChildHeartBeat  = "child_heartbeat"
	ModuleStatus          = "status"
	ModuleStatic          = "static"
	ModuleHTTP            = "http"
	ModuleMetricbeat      = "metricbeat"
	ModulePing            = "ping"
	ModuleScript          = "script"
	ModuleTCP             = "tcp"
	ModuleUDP             = "udp"
	ModuleKeyword         = "keyword"
	ModuleTrap            = "snmptrap"
	ModuleBasereport      = "basereport"
	ModuleExceptionbeat   = "exceptionbeat"
	ModuleKubeevent       = "kubeevent"
	ModuleProcessbeat     = "processbeat"
	ModuleProcConf        = "procconf"
	ModuleProcCustom      = "proccustom"
	ModuleProcSync        = "procsync"
	ModuleProcStatus      = "procstatus"
	ModuleProcBin         = "procbin"
	ModuleLoginLog        = "loginlog"
	ModuleProcSnapshot    = "procsnapshot"
	ModuleSocketSnapshot  = "socketsnapshot"
	ModuleShellHistory    = "shellhistory"
	ModuleRpmPackage      = "rpmpackage"
	ModuleTimeSync        = "timesync"
	ModuleDmesg           = "dmesg"
	ModuleSelfStats       = "selfstats"
)

const (
	UTCTimeFormat = "2006-01-02 15:04:05"
)

// Status :
type Status int

// task status
const (
	TaskReady    Status = iota
	TaskRunning  Status = iota
	TaskError    Status = iota
	TaskFinished Status = iota
)

// Task : task for scheduler
type Task interface {
	GetTaskID() int32
	GetStatus() Status
	SetConfig(TaskConfig)
	GetConfig() TaskConfig
	SetGlobalConfig(Config)
	GetGlobalConfig() Config
	Reload()
	Wait()
	Stop()
	Run(ctx context.Context, e chan<- Event)
}
