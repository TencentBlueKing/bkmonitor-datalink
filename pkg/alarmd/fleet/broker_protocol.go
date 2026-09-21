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
	"fmt"
	"strings"
)

// Reading the brokers' answer against what this client sends.
//
// A Kafka client built for a newer protocol than its brokers speak is not
// told so: the broker closes the connection on the first request it does not
// understand, and the client reads end of file. On a live deployment every
// event write did that for an hour after a release raised the client's
// version by three minor steps, while the readiness probe -- a metadata
// request both sides agreed on -- kept the role ready and the page's checks
// all ok, because the checks judged the configured version against itself.
// The brokers can be asked which request versions they accept; this is the
// reading of their answer, so the check on the page is decided by the
// broker and not by the configuration.

// ProduceVersionCheck reads each broker's accepted Produce request versions
// against the one this client sends. asked is false while the sink has not
// opened and so has asked nobody: the check then fails and says so, rather
// than being absent, because a missing row on this list read as fine for an
// hour.
func ProduceVersionCheck(want int16, brokers []BrokerProtocol, asked bool) EndpointCheck {
	check := EndpointCheck{Name: EndpointCheckProduceVersion}
	if !asked {
		check.Detail = "brokers not asked yet: the output sink has not opened"
		return check
	}
	if len(brokers) == 0 {
		check.Detail = "the client listed no broker to ask"
		return check
	}
	var refusing []string
	for _, broker := range brokers {
		switch {
		case !broker.Answered:
			refusing = append(refusing, fmt.Sprintf("%s (id %d) did not answer which versions it accepts: %s", broker.Address, broker.ID, broker.Error))
		case want < broker.ProduceMinVersion || want > broker.ProduceMaxVersion:
			refusing = append(refusing, fmt.Sprintf("%s (id %d) accepts Produce v%d..v%d; this client sends v%d",
				broker.Address, broker.ID, broker.ProduceMinVersion, broker.ProduceMaxVersion, want))
		}
	}
	if len(refusing) > 0 {
		check.Detail = strings.Join(refusing, "; ")
		return check
	}
	check.OK = true
	return check
}

// brokerCappedHeaders is the output entry of a replica whose client asked its
// brokers and came away on a version that cannot carry record headers: the
// negotiated version is written and the headers verdict is no. A replica that
// has not asked yet, or whose configured version was already below the line,
// is not this case.
func brokerCappedHeaders(output *Endpoint) bool {
	return output != nil && output.NegotiatedVersion != "" && output.HeadersSupported != nil && !*output.HeadersSupported
}

// checkDetail is the sentence a replica wrote beside a named check, or empty.
func checkDetail(output *Endpoint, name string) string {
	for _, check := range output.Checks {
		if check.Name == name {
			return check.Detail
		}
	}
	return ""
}
