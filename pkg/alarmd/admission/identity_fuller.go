// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package admission

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"
)

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
	// The naming is what the record itself spelled, read the way Python's host
	// status filter branches on it: bk_host_id and bk_target_ip by presence,
	// their values by truthiness (filters.py:85-116). It is the starting point
	// only. Python's fuller runs before that filter and writes into the record
	// the host it found - by id, by service instance, or by an address spelled
	// ip or given without its cloud - and the filter then judges that host
	// (fullers.py:55-110). The CMDB fullers make the same change to the naming
	// here, so a record whose host only the fuller could find is judged by
	// the host it found, and one whose host it could not find is left as the
	// record spelled it.
	targetAddress := TruthyDimension(dimensions, "bk_target_ip")
	hostIDText := TruthyDimension(dimensions, "bk_host_id")
	_, addressNamed := dimensions["bk_target_ip"]
	_, cloudNamed := dimensions["bk_target_cloud_id"]
	_, hostIDNamed := dimensions["bk_host_id"]
	facts.HostNaming = HostNaming{
		NamedID: hostIDNamed, NamedAddress: addressNamed, NamedCloud: cloudNamed,
		Usable: targetAddress != "" || hostIDText != "", IDKey: hostIDText,
		AddressKey: LookupAddressKey(targetAddress, dimensionText(dimensions, "bk_target_cloud_id")),
	}

	// The keys a host target matches, as Python's TargetCondition builds them
	// from the record (target.py:112-120): the id when it is truthy, and the
	// address read by presence - bk_target_ip when the dimension is there,
	// else ip - with its cloud read the same way, defaulting to 0 only when
	// neither cloud dimension is there. Deliberately not coerced: the target
	// match takes the value as it stands, and only the host status filter's
	// lookup coerces (HostNaming.AddressKey). A fuller that finds the host
	// rewrites these keys the way Python's rewrites the record.
	if address := TargetAddressKey(dimensions, dimensionText(dimensions, "bk_target_cloud_id")); address != "" {
		facts.AddHostKey(address)
	}
	if hostIDText != "" {
		facts.AddHostKey(hostIDText)
	}

	// A topology condition on a record no fuller placed reads the record's
	// own bk_obj_id and bk_inst_id (target.py:128-141): log keyword and
	// CMDB-level series are aggregated by them (strategy.py:348-351). A
	// fuller that places a host or an instance replaces this with its chain,
	// as Python's written bk_topo_node takes precedence over the record's.
	_, objectNamed := dimensions["bk_obj_id"]
	_, instanceNamed := dimensions["bk_inst_id"]
	if objectNamed && instanceNamed {
		facts.SetTopoNodes([]string{dimensionText(dimensions, "bk_obj_id") + "|" + dimensionText(dimensions, "bk_inst_id")})
	}

	serviceInstance := dimensionText(dimensions, "bk_target_service_instance_id")
	if serviceInstance == "" {
		serviceInstance = dimensionText(dimensions, "service_instance_id")
	}
	facts.AddServiceInstanceKey(serviceInstance)
}

// TargetAddressKey is the "ip|cloud" key Python's TargetCondition reads from a
// record: data.get("bk_target_ip", data.get("ip")) for the address, empty
// when that is falsy, and for the cloud data.get("bk_target_cloud_id",
// data.get("bk_cloud_id", 0)). The cloud is passed in because a fuller that
// found the host writes bk_target_cloud_id, and the key is then built from
// the written value; pass the record's own when nothing was written.
func TargetAddressKey(dimensions map[string]json.RawMessage, targetCloud string) string {
	address := ""
	if _, present := dimensions["bk_target_ip"]; present {
		address = TruthyDimension(dimensions, "bk_target_ip")
	} else {
		address = TruthyDimension(dimensions, "ip")
	}
	if address == "" {
		return ""
	}
	cloud := "0"
	if _, present := dimensions["bk_target_cloud_id"]; present || targetCloud != "" {
		cloud = targetCloud
	} else if _, present := dimensions["bk_cloud_id"]; present {
		cloud = dimensionText(dimensions, "bk_cloud_id")
	}
	return address + "|" + cloud
}

// TruthyDimension is a dimension's text when Python would take its value as
// true, and empty otherwise: absent, null, false, an empty string and a zero
// are all false to `if value:` and to `value or fallback`, which is how
// Python's fuller and host status filter read the identities a record
// carries. A zero host id is therefore no id at all, as it is there.
func TruthyDimension(dimensions map[string]json.RawMessage, name string) string {
	raw, found := dimensions[name]
	if !found {
		return ""
	}
	switch trimmed := strings.TrimSpace(string(raw)); trimmed {
	case "", "null", "false", "[]", "{}", `""`:
		return ""
	}
	var number float64
	if err := json.Unmarshal(raw, &number); err == nil && number == 0 {
		return ""
	}
	return dimensionText(dimensions, name)
}

// LookupAddressKey builds the key Python's address lookup uses: the target
// address with its target cloud coerced by safe_int. The ip / bk_cloud_id
// spellings are deliberately not read here even though the target-scope key
// below falls back to them - Python never looks a host up by those, and a
// lookup key that differs from Python's decides the host status filter on a
// host Python never consulted.
func LookupAddressKey(address string, cloud string) string {
	if address == "" {
		return ""
	}
	return address + "|" + safeIntText(cloud, "0")
}

// safeIntText mirrors bkmonitor.utils.common_utils.safe_int: an integer, else
// an integer parsed through a float, else the fallback. It exists because the
// identities in a series are whatever the platform emitted, and the platform
// does not guarantee they are numbers.
func safeIntText(text string, fallback string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return fallback
	}
	if value, err := strconv.ParseInt(text, 10, 64); err == nil {
		return strconv.FormatInt(value, 10)
	}
	if value, err := strconv.ParseFloat(text, 64); err == nil && !math.IsInf(value, 0) && !math.IsNaN(value) {
		return strconv.FormatInt(int64(value), 10)
	}
	return fallback
}

// FullerAddress is the address Python's fuller looks a host up by: bk_target_ip
// or ip, whichever is true first, in bk_target_cloud_id or bk_cloud_id or
// "0" the same way. Truthiness, not presence: an empty bk_target_ip falls
// through to ip, and a zero cloud dimension falls through to the next. The
// target match reads the same dimensions by presence instead, which is why
// the two are built separately.
func FullerAddress(dimensions map[string]json.RawMessage) (string, string) {
	address := TruthyDimension(dimensions, "bk_target_ip")
	if address == "" {
		address = TruthyDimension(dimensions, "ip")
	}
	cloud := TruthyDimension(dimensions, "bk_target_cloud_id")
	if cloud == "" {
		cloud = TruthyDimension(dimensions, "bk_cloud_id")
	}
	if cloud == "" {
		cloud = "0"
	}
	return address, cloud
}
