// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package storecensus

import (
	"strings"
	"testing"
)

// Keys that differ only in which thing they are about are one family; keys
// of different kinds are not. A family keeps the words of the code that
// wrote the key and nothing else.
func TestAKeysFamilyIsItsKindNotItsInstance(t *testing.T) {
	for key, want := range map[string]string{
		"alarmd:control:object-catalog:qgobj:" + strings.Repeat("ab", 32): "alarmd:control:object-catalog:qgobj:*",
		"alarmd:control:object-catalog:activation":                        "alarmd:control:object-catalog:activation",
		"alarmd:state:{qg-1}:series:0123456789abcdef":                     "alarmd:state:*:series:*",
		"alarmd:state:{qg.1:a}:series":                                    "alarmd:state:*:series",
		"alarmd:qg:550e8400-e29b-41d4-a716-446655440000:state":            "alarmd:qg:*:state",
		"alarmd:strategy:12345":                                           "alarmd:strategy:*",
		"alarmd:biz:-12345:cache":                                         "alarmd:biz:*:cache",
		"alarmd:deadbeef:cache":                                           "alarmd:*:cache",
		"alarmd:runtime:v1:runtime3:v2:9f:ab":                             "alarmd:runtime:v1:runtime3:v2:*:*",
		"alarmd:state:series:gap:v2:progress:slot:worker:session:grant":   "alarmd:state:series:gap:v2:progress:slot:worker:*",
		"celery":                "celery",
		"_kombu.binding.celery": "_kombu.binding.celery",
		"celery-task-meta-550e8400-e29b-41d4-a716-446655440000": "celery-task-meta-*",
		// Not a UUID at its end: the parts that are words stay.
		"celery-task-meta-550e8400xe29bx41d4xa716x446655440000": "celery-task-meta-*",
	} {
		if got := FamilyOf(key); got != want {
			t.Errorf("FamilyOf(%q) = %q, want %q", key, got, want)
		}
	}
	if got := FamilyOf(strings.Repeat("x", 200)); got != "*" {
		t.Errorf("a long token = %q, want *", got)
	}
	if got := FamilyOf(strings.Repeat("state:", 5) + strings.Repeat("y", 39)); len(got) > maxFamilyName {
		t.Errorf("family name %q is %d bytes, past %d", got, len(got), maxFamilyName)
	}
}

// The platform's Python cache writes its keys with "." between segments,
// under a prefix of its application, platform and environment, and a
// cluster's name when it runs as one: those keys fold by kind as alarmd's
// do, and two of different kinds stay two.
func TestAPythonCacheKeysFamilyIsItsKind(t *testing.T) {
	md5 := strings.Repeat("0f", 16)
	for key, want := range map[string]string{
		"bk_monitorv3.ee.detect.result.1234.5678." + md5 + ".1":           "bk_monitorv3.ee.detect.result.*.*.*.*",
		"bk_monitorv3.ee.default.detect.result.99.1." + md5 + ".2":        "bk_monitorv3.ee.default.detect.result.*.*.*.*",
		"bk_monitorv3.ee.detect.new_series.seen.1234.5678." + md5:         "bk_monitorv3.ee.detect.new_series.seen.*.*.*",
		"bk_monitorv3.ee.cache.strategy.1234":                             "bk_monitorv3.ee.cache.strategy.*",
		"bk_monitorv3.ee.checkpoint.strategy_group_" + md5:                "bk_monitorv3.ee.checkpoint.strategy_group_*",
		"bk_monitorv3.ee.trigger.lock.1234_5678":                          "bk_monitorv3.ee.trigger.lock.*",
		"bk_monitorv3.ee.cache.action_config.config_id_15":                "bk_monitorv3.ee.cache.action_config.config_id_*",
		"bk_monitorv3.ee.selfmonitor.redis.strategy_cost.snapshot.node_3": "bk_monitorv3.ee.selfmonitor.redis.strategy_cost.snapshot.node_*",
	} {
		if got := FamilyOf(key); got != want {
			t.Errorf("FamilyOf(%q) = %q, want %q", key, got, want)
		}
	}
	if FamilyOf("bk_monitorv3.ee.detect.result.1.2."+md5+".1") == FamilyOf("bk_monitorv3.ee.detect.new_series.seen.1.2."+md5) {
		t.Error("detect.result and new_series.seen are one family, want two")
	}
}

// Whatever a key's writer put in it from data - a receiver's name, a mail
// address, text in another script, a strategy's dotted name - is not a word
// of the code and is written "*": a family name is a metric label.
func TestDataInAKeyNeverNamesItsFamily(t *testing.T) {
	for key, want := range map[string]string{
		// A receiver, lower-case letters like any word of kind.
		"bk_monitorv3.ee.fta_action.sub_converge.3.abnormal.chatx.alice": "bk_monitorv3.ee.fta_action.sub_converge.*.abnormal.*.*",
		"bk_monitorv3.ee.action.notice.phone_collect.alice":              "bk_monitorv3.ee.action.notice.phone_collect.*",
		// A mail address, split by its dots.
		"bk_monitorv3.ee.fta_action.notice.abnormal.mail.alice@example.test": "bk_monitorv3.ee.fta_action.notice.abnormal.mail.*.*",
		// Text in another script.
		"alarmd:strategy:订单服务核心链路:state": "alarmd:strategy:*:state",
		// A dotted free-text name.
		"bk_monitorv3.ee.cache.strategy.payment-gw.orders.eu-west": "bk_monitorv3.ee.cache.strategy.*.*.*",
		// An address in a colon key: its domain ends at the next ":".
		"alarmd:notify:alice@example.test:state": "alarmd:notify:*.*:state",
		// A pod name: its words that are code stay, its instance does not.
		"alarmd:worker:alice-trigger-5f6c7d8e9-xq7mz": "alarmd:worker:*",
		"alarmd:worker:trigger-alice-5f6c7d8e9-xq7mz": "alarmd:worker:trigger-*",
		// A user's table name after the code's words: the rest of it is one "*".
		"bk_monitorv3.ee.cache.result_table_bk_data_100_aiops_samplemodel_forecast_0a1b2c3d_ri": "bk_monitorv3.ee.cache.result_table_bk_data_*_aiops_*",
		"bk_monitorv3.ee.cache.result_table_bk_data_100_zeta_dwd_sales_orders_di":               "bk_monitorv3.ee.cache.result_table_bk_data_*_*",
		"notice_receiver_alice": "notice_receiver_*",
		"target_192.0.2.10|0":   "target_*.*.*.*",
	} {
		got := FamilyOf(key)
		if got != want {
			t.Errorf("FamilyOf(%q) = %q, want %q", key, got, want)
		}
		for _, value := range []string{"alice", "example", "订单", "payment", "orders", "xq7mz", "samplemodel",
			"forecast", "zeta", "sales"} {
			if strings.Contains(got, value) {
				t.Errorf("FamilyOf(%q) = %q carries %q", key, got, value)
			}
		}
	}
}

// A deployment's configured key prefixes are its own: their segments stay as
// they are spelled, an environment in brackets included, where the built-in
// words alone would fold them.
func TestAConfiguredPrefixIsKeptAsItIs(t *testing.T) {
	key := "bk_monitorv3.ee[stag].selfmonitor.redis.strategy_cost.snapshot.node_3"
	if got := FamilyOf(key); got != "bk_monitorv3.*.selfmonitor.redis.strategy_cost.snapshot.node_*" {
		t.Errorf("without the prefix configured = %q", got)
	}
	if got := NewVocabulary("bk_monitorv3.ee[stag]").FamilyOf(key); got != "bk_monitorv3.ee[stag].selfmonitor.redis.strategy_cost.snapshot.node_*" {
		t.Errorf("with the prefix configured = %q", got)
	}
	if got := NewVocabulary("tenantprefix:").FamilyOf("tenantprefix:alarmd:alice"); got != "tenantprefix:alarmd:*" {
		t.Errorf("a configured prefix's segment = %q", got)
	}
	// Its words are the deployment's too, wherever a key repeats them.
	if got := NewVocabulary("tenantprefix:").FamilyOf("alarmd:tenantprefix_cache:1"); got != "alarmd:tenantprefix_cache:*" {
		t.Errorf("a configured prefix's word inside another segment = %q", got)
	}
}
