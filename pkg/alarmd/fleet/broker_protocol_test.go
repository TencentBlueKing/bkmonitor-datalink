// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fleet

import (
	"strings"
	"testing"
)

// The check on the page is decided by what the brokers answered, not by the
// configuration: a broker whose accepted range excludes the version this
// client sends fails it by name, one that did not answer fails it with the
// client's words, and before anyone was asked the row is there and failing
// rather than absent.
func TestTheProduceVersionCheckIsDecidedByTheBrokersAnswer(t *testing.T) {
	accepting := BrokerProtocol{Address: "broker-1:9092", ID: 1, Answered: true, ProduceMinVersion: 0, ProduceMaxVersion: 7}
	old := BrokerProtocol{Address: "broker-3:9092", ID: 3, Answered: true, ProduceMinVersion: 0, ProduceMaxVersion: 2}
	silent := BrokerProtocol{Address: "broker-2:9092", ID: 2, Answered: false, Error: "EOF"}
	for name, test := range map[string]struct {
		want    int16
		brokers []BrokerProtocol
		asked   bool
		ok      bool
		detail  []string
	}{
		"not asked yet": {want: 3, brokers: nil, asked: false, detail: []string{"not asked yet"}},
		// Asked, and the client had nobody to ask: also not ok, and not the
		// same sentence, because the two are different states of the sink.
		"asked with no brokers":                {want: 3, brokers: []BrokerProtocol{}, asked: true, detail: []string{"no broker"}},
		"every broker accepts":                 {want: 3, brokers: []BrokerProtocol{accepting, accepting}, asked: true, ok: true},
		"the version is the top of a range":    {want: 2, brokers: []BrokerProtocol{old}, asked: true, ok: true},
		"the version is the bottom of a range": {want: 0, brokers: []BrokerProtocol{old}, asked: true, ok: true},
		"one broker's range stops short": {want: 3, brokers: []BrokerProtocol{accepting, old}, asked: true,
			detail: []string{"broker-3:9092 (id 3)", "v0..v2", "sends v3"}},
		"one broker did not answer": {want: 3, brokers: []BrokerProtocol{accepting, silent}, asked: true,
			detail: []string{"broker-2:9092 (id 2)", "did not answer", "EOF"}},
		"both kinds of refusal are named": {want: 3, brokers: []BrokerProtocol{old, silent}, asked: true,
			detail: []string{"broker-3:9092", "broker-2:9092"}},
		"a range that starts above the version": {want: 1, brokers: []BrokerProtocol{{Address: "broker-9:9092", ID: 9, Answered: true, ProduceMinVersion: 2, ProduceMaxVersion: 4}}, asked: true,
			detail: []string{"v2..v4", "sends v1"}},
	} {
		name, test := name, test
		t.Run(name, func(t *testing.T) {
			check := ProduceVersionCheck(test.want, test.brokers, test.asked)
			if check.Name != EndpointCheckProduceVersion {
				t.Fatalf("check name = %q", check.Name)
			}
			if check.OK != test.ok {
				t.Fatalf("check = %+v, want ok %t", check, test.ok)
			}
			if test.ok && check.Detail != "" {
				t.Fatalf("a passing check carries a sentence: %q", check.Detail)
			}
			for _, fragment := range test.detail {
				if !strings.Contains(check.Detail, fragment) {
					t.Fatalf("check detail = %q, want it to name %q", check.Detail, fragment)
				}
			}
		})
	}
	// Only the refusing brokers are named: the accepting one beside them is
	// not in the sentence.
	if got := ProduceVersionCheck(3, []BrokerProtocol{accepting, old}, true).Detail; strings.Contains(got, accepting.Address) {
		t.Fatalf("an accepting broker was named among the refusing: %q", got)
	}
}

// The check name is on the closed list, so the page's wording table has a
// row for it.
func TestTheProduceVersionCheckIsAKnownCheckName(t *testing.T) {
	for _, name := range EndpointCheckNames {
		if name == EndpointCheckProduceVersion {
			return
		}
	}
	t.Fatalf("EndpointCheckNames = %v lacks %q", EndpointCheckNames, EndpointCheckProduceVersion)
}
