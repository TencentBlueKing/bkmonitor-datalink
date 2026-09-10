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
	// The naming is read from the platform's own spellings only - bk_target_ip,
	// bk_target_cloud_id, bk_host_id - because the host status filter branches
	// on exactly those. The ip / bk_cloud_id fallbacks below are a convenience
	// for building target-scope keys; letting them count as "this record names
	// a host" would make a record Python treats as non-host data eligible for
	// a CMDB lookup, and a host in a disabled state would then drop a series
	// Python keeps. That is a missed alert, so the two readings stay separate.
	targetAddress := dimensionText(dimensions, "bk_target_ip")
	hostIDText := dimensionText(dimensions, "bk_host_id")
	_, addressNamed := dimensions["bk_target_ip"]
	_, cloudNamed := dimensions["bk_target_cloud_id"]
	_, hostIDNamed := dimensions["bk_host_id"]
	facts.HostNaming = HostNaming{
		NamedID: hostIDNamed, NamedAddress: addressNamed, NamedCloud: cloudNamed,
		Usable: targetAddress != "" || hostIDText != "",
	}

	address := targetAddress
	cloud := dimensionText(dimensions, "bk_target_cloud_id")
	if address == "" {
		address = dimensionText(dimensions, "ip")
	}
	if cloud == "" {
		cloud = dimensionText(dimensions, "bk_cloud_id")
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
