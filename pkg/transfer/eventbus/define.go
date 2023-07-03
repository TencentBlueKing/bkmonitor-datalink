// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package eventbus

var (
	EvSysPreRun          = "sys:pre-run"
	EvSysPostRun         = "sys:post-run"
	EvSysConfigPreParse  = "sys:conf-pre-parse"
	EvSysConfigPostParse = "sys:conf-post-parse"
	EvSysLoggerReady     = "sys:logger-ready"
	EvSysExit            = "sys:exit"
	EvSysKill            = "sys:kill"
	EvSysFatal           = "sys:fatal"
	EvSysUpdate          = "sys:update"
	EvSysLimitCPU        = "sys:limit:cpu"
	EvSysLimitFile       = "sys:limit:fd"
	EvSysLimitMemory     = "sys:limit:mem"

	EvRunnerPreRun  = "runner:pre-run"
	EvRunnerPostRun = "runner:post-run"

	EvDataIDConfig = "dataid:dynamic-notify"
	EvConsul       = "consul:original"

	EvSigUpdateCCCache  = "signal-update-cc-cache"
	EvSigUpdateCCWorker = "signal-update-cc-worker"
	EvSigCommitCache    = "signal-commit-cache"
	EvSigUpdateMemCache = "signal-update-mem-cache"

	EvSigDumpHostInfo     = "signal-dump-host-info"
	EvSigDumpInstanceInfo = "signal-dump-instance-info"
	EvSigSetLogLevel      = "signal-set-log-level"
	EvSigDumpStack        = "signal-dump-stack"
	EvSigSetBlockProfile  = "signal-set-block-profile"
	EvSigLimitResource    = "signal-limit-resource"
)
