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
	"sort"
	"sync"
	"time"
)

// MaxAddressDiffersSamples bounds the samples a replica keeps and the
// deployment shows.
const MaxAddressDiffersSamples = 16

// AddressDiffersSample is one admitted record whose host CMDB placed by id
// or agent and whose own bk_target_ip is not that host's address: when, how
// it differs (differs, missing, undecodable), the strategy and Query Group
// that read it, the host, what the record reported (the value, or the cause
// when there is no value) and CMDB's address. The count says how often; this
// says which.
type AddressDiffersSample struct {
	At          time.Time `json:"at"`
	Cause       string    `json:"cause"`
	StrategyID  string    `json:"strategy_id"`
	Tenant      string    `json:"tenant,omitempty"`
	Business    string    `json:"business"`
	QueryGroup  string    `json:"query_group"`
	HostID      string    `json:"host_id,omitempty"`
	AgentID     string    `json:"agent_id,omitempty"`
	Reported    string    `json:"reported"`
	CMDBAddress string    `json:"cmdb_address"`
	// Replica is the replica that saw it, set on the deployment's list.
	Replica string `json:"replica,omitempty"`
}

// AddressDiffersSamples keeps a replica's latest samples, one per strategy
// and host, newest first. Only a record whose address differs reaches it,
// so its lock is off the path every placed record takes.
type AddressDiffersSamples struct {
	mu      sync.Mutex
	samples []AddressDiffersSample
}

// Record keeps a sample, in place of an earlier one of the same strategy and
// host, and drops the oldest past the bound. The samples shift in place, so
// once the bound is reached a record allocates nothing.
func (s *AddressDiffersSamples) Record(sample AddressDiffersSample) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	end := len(s.samples)
	for index, earlier := range s.samples {
		if earlier.StrategyID == sample.StrategyID && earlier.HostID == sample.HostID && earlier.AgentID == sample.AgentID {
			end = index
			break
		}
	}
	if end == len(s.samples) {
		if len(s.samples) < MaxAddressDiffersSamples {
			s.samples = append(s.samples, AddressDiffersSample{})
		} else {
			end = len(s.samples) - 1
		}
	}
	copy(s.samples[1:end+1], s.samples[:end])
	s.samples[0] = sample
}

// Latest is a copy of the samples, newest first; nil when there are none.
func (s *AddressDiffersSamples) Latest() []AddressDiffersSample {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.samples) == 0 {
		return nil
	}
	return append([]AddressDiffersSample(nil), s.samples...)
}

// aggregateAddressDiffers puts the replicas' samples together, newest first,
// each naming its replica, within the bound.
func aggregateAddressDiffers(view *View, snapshots []Snapshot) {
	var all []AddressDiffersSample
	for _, snapshot := range snapshots {
		for _, sample := range snapshot.AddressDiffers {
			sample.Replica = snapshot.Replica
			all = append(all, sample)
		}
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].At.After(all[j].At) })
	if len(all) > MaxAddressDiffersSamples {
		all = all[:MaxAddressDiffersSamples]
	}
	view.AddressDiffers = all
}
