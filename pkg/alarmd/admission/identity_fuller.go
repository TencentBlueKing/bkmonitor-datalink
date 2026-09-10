// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package admission

import "encoding/json"

// IdentityFuller reads the identities a series carries in its own dimensions.
// It touches no external store, so it always runs and always produces the same
// answer for the same series - which is what lets a later fuller be added or
// removed without changing what "this series has no host at all" means.
//
// The dimension spellings are the ones the platform emits: a host is named
// either by address and cloud, or by host id, and both are kept because a
// strategy target may name either.
type IdentityFuller struct{}

func (IdentityFuller) Name() string { return "identity" }

func (IdentityFuller) Fill(dimensions map[string]json.RawMessage, facts *Facts) {
	address := dimensionText(dimensions, "bk_target_ip")
	cloud := dimensionText(dimensions, "bk_target_cloud_id")
	_, addressNamed := dimensions["bk_target_ip"]
	_, cloudNamed := dimensions["bk_target_cloud_id"]
	if address == "" {
		address = dimensionText(dimensions, "ip")
	}
	if !addressNamed {
		_, addressNamed = dimensions["ip"]
	}
	if cloud == "" {
		cloud = dimensionText(dimensions, "bk_cloud_id")
	}
	if !cloudNamed {
		_, cloudNamed = dimensions["bk_cloud_id"]
	}
	hostIDText := dimensionText(dimensions, "bk_host_id")
	_, hostIDNamed := dimensions["bk_host_id"]
	facts.HostNaming = HostNaming{
		NamedID: hostIDNamed, NamedAddress: addressNamed, NamedCloud: cloudNamed,
		Usable: address != "" || hostIDText != "",
	}
	if address != "" {
		if cloud == "" {
			// Python defaults an absent cloud to the direct area, and the
			// CMDB cache keys hosts the same way.
			cloud = "0"
		}
		facts.AddHostKey(address + "|" + cloud)
	}
	if hostIDText != "" {
		facts.AddHostKey(hostIDText)
	}

	serviceInstance := dimensionText(dimensions, "bk_target_service_instance_id")
	if serviceInstance == "" {
		serviceInstance = dimensionText(dimensions, "service_instance_id")
	}
	facts.AddServiceInstanceKey(serviceInstance)
}
