// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package elasticsearch

import (
	"net"
	"net/http"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/eventbus"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/pipeline"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

// ConfKeyDefaultVersion
const (
	ConfKeyDefaultVersion        = "elasticsearch.default_version"
	ConfKeyMaxIdleConns          = "elasticsearch.net.max_idle_connections"
	ConfKeyMaxIdleConnsTotal     = "elasticsearch.net.max_idle_connections_total"
	ConfKeyIdleConnTimeout       = "elasticsearch.net.idle_connection_timeout"
	ConfKeyTLSHandshakeTimeout   = "elasticsearch.net.tls_handshake_timeout"
	ConfKeyExpectContinueTimeout = "elasticsearch.net.expect_continue_timeout"
	ConfKeyDialTimeout           = "elasticsearch.net.dial_timeout"
	ConfKeyDialKeepAlive         = "elasticsearch.net.dial_keep_alive_period"
)

func initConfiguration(c define.Configuration) {
	c.SetDefault(ConfKeyDefaultVersion, "5.4")
	c.SetDefault(ConfKeyMaxIdleConns, 100)
	c.SetDefault(ConfKeyMaxIdleConnsTotal, 300)
	c.SetDefault(ConfKeyIdleConnTimeout, 3*time.Minute)
	c.SetDefault(ConfKeyTLSHandshakeTimeout, 10*time.Second)
	c.SetDefault(ConfKeyExpectContinueTimeout, 1*time.Second)
	c.SetDefault(ConfKeyDialTimeout, 30*time.Second)
	c.SetDefault(ConfKeyDialKeepAlive, time.Hour)

	c.RegisterAlias("elasticsearch.backend.channel_size", pipeline.ConfKeyPipelineChannelSize)
	c.RegisterAlias("elasticsearch.backend.wait_delay", pipeline.ConfKeyPipelineFrontendWaitDelay)
	c.RegisterAlias("elasticsearch.backend.buffer_size", pipeline.ConfKeyPayloadBufferSize)
	c.RegisterAlias("elasticsearch.backend.flush_interval", pipeline.ConfKeyPayloadFlushInterval)
	c.RegisterAlias("elasticsearch.backend.flush_reties", pipeline.ConfKeyPayloadFlushReties)
	c.RegisterAlias("elasticsearch.backend.max_concurrency", pipeline.ConfKeyPayloadFlushConcurrency)
}

func readConfiguration(c define.Configuration) {
	dialer := &net.Dialer{
		Timeout:   c.GetDuration(ConfKeyDialTimeout),
		KeepAlive: c.GetDuration(ConfKeyDialKeepAlive),
	}
	DefaultTransport = &http.Transport{
		MaxIdleConnsPerHost:   c.GetInt(ConfKeyMaxIdleConns),
		MaxIdleConns:          c.GetInt(ConfKeyMaxIdleConnsTotal),
		IdleConnTimeout:       c.GetDuration(ConfKeyIdleConnTimeout),
		TLSHandshakeTimeout:   c.GetDuration(ConfKeyTLSHandshakeTimeout),
		ExpectContinueTimeout: c.GetDuration(ConfKeyExpectContinueTimeout),
		DialContext:           dialer.DialContext,
	}
}

func init() {
	utils.CheckError(eventbus.Subscribe(eventbus.EvSysConfigPreParse, initConfiguration))
	utils.CheckError(eventbus.Subscribe(eventbus.EvSysConfigPostParse, readConfiguration))
}
