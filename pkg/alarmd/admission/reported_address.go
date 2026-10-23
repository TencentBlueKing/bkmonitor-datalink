// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package admission

// The ways a record's own bk_target_ip compares with the address of the host
// CMDB placed it at by id or agent: closed, for a counter label.
const (
	AddressSame        = "same"
	AddressDiffers     = "differs"
	AddressMissing     = "missing"
	AddressUndecodable = "undecodable"
)

// AddressCauses are the ways an address differs, the label values of the
// count of records that do.
var AddressCauses = []string{AddressDiffers, AddressMissing, AddressUndecodable}

// MaxReportedAddressBytes bounds the value a record reported, as a sample
// carries it.
const MaxReportedAddressBytes = 64

// ReportedAddress is how a record placed by id or agent reports its address
// against CMDB's (Facts.ReportedAddress). Cause is empty for a record not
// placed that way. The rest is filled only when the address is not the
// same: Reported is the record's own value when it is another address, cut
// to MaxReportedAddressBytes; CMDB is the host's address; HostID and AgentID
// name the host. A comparison that finds the same address fills nothing, so
// the path every such record takes allocates nothing.
type ReportedAddress struct {
	Cause    string
	Reported string
	CMDB     string
	HostID   string
	AgentID  string
}

// Differs is whether the record's address is not its host's in any way.
func (address ReportedAddress) Differs() bool {
	return address.Cause != "" && address.Cause != AddressSame
}
