// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package cmdbcache

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/admission"
)

const (
	instanceSeven    = `{"service_instance_id":7,"bk_host_id":720002,"ip":"192.0.2.172","bk_cloud_id":0,"topo_link":{"module|91":[{"bk_obj_id":"module","bk_inst_id":91}]}}`
	instanceNoIP     = `{"service_instance_id":8,"bk_host_id":720009,"topo_link":{"module|91":[{"bk_obj_id":"module","bk_inst_id":91}]}}`
	wantInstanceOver = `{"bk_target_cloud_id":0,"bk_target_ip":"192.0.2.172"}`
)

// instanceLoad is one load holding the host and the instances given, with
// instance records refused beside them.
func instanceLoad(instances []string, published time.Time, refusedInstances int) func() (*Index, error) {
	return func() (*Index, error) {
		builder := newIndexBuilder(published)
		builder.addFields([]string{"720002", identityLive})
		builder.addServiceInstanceFields(instances)
		index := builder.index
		index.byAgent = map[string]string{}
		index.publishedAt = published
		index.refused.ServiceInstances += refusedInstances
		return index, nil
	}
}

// overlaid is the record's host overlay as JSON, and where it was read.
func (c *identityCase) overlaid() (string, string) {
	c.t.Helper()
	facts := c.chain.Enrich(c.record)
	encoded, _ := json.Marshal(facts.HostOverlay)
	return string(encoded), facts.HostOverlaySource
}

// A record placed through its service instance reads the instance's address
// and cloud, as Python's fuller writes them (fullers.py:76-89): the cloud a
// number, no bk_host_id. A record already naming them carries nothing.
func TestAnInstancePlacedRecordReadsTheInstancesAddress(t *testing.T) {
	c := newIdentityCase(t, `{"bk_target_service_instance_id":"7","bk_target_ip":"198.51.100.9","bk_target_cloud_id":"0"}`)
	c.refresh(instanceLoad([]string{"7", instanceSeven}, c.now, 0))
	if got, source := c.overlaid(); got != wantInstanceOver || source != "" {
		t.Fatalf("overlay %s (source %q), want %s from the index in force", got, source, wantInstanceOver)
	}
	same := newIdentityCase(t, `{"service_instance_id":"7","bk_target_ip":"192.0.2.172","bk_target_cloud_id":0}`)
	same.refresh(instanceLoad([]string{"7", instanceSeven}, same.now, 0))
	if got, _ := same.overlaid(); got != "null" {
		t.Fatalf("a record naming the instance's own address: overlay %s, want none", got)
	}
}

// A stale index, an empty one, and one whose instance hash is empty still
// overlay an instance-placed record from the last load that held instances.
func TestAStaleOrEmptyIndexKeepsAnInstancePlacedRecordsIdentity(t *testing.T) {
	record := `{"bk_target_service_instance_id":"7","bk_target_ip":"198.51.100.9"}`
	stale := newIdentityCase(t, record)
	published := stale.now
	stale.refresh(instanceLoad([]string{"7", instanceSeven}, published, 0))
	stale.now = published.Add(16 * time.Minute)
	if got, source := stale.overlaid(); got != wantInstanceOver || source != admission.HostOverlayFromStaleIndex {
		t.Fatalf("stale index: overlay %s (source %q), want %s from the stale index", got, source, wantInstanceOver)
	}

	empty := newIdentityCase(t, record)
	empty.refresh(instanceLoad([]string{"7", instanceSeven}, empty.now, 0))
	empty.refresh(identityLoad(nil, map[string]string{}, empty.now, 0))
	if got, source := empty.overlaid(); got != wantInstanceOver || source != admission.HostOverlayFromLastPopulatedIndex {
		t.Fatalf("empty index: overlay %s (source %q), want %s from the last populated index", got, source, wantInstanceOver)
	}

	noInstances := newIdentityCase(t, record)
	noInstances.refresh(instanceLoad([]string{"7", instanceSeven}, noInstances.now, 0))
	noInstances.refresh(instanceLoad(nil, noInstances.now, 0))
	if got, source := noInstances.overlaid(); got != wantInstanceOver || source != admission.HostOverlayFromLastPopulatedIndex {
		t.Fatalf("instance hash empty: overlay %s (source %q), want %s from the last instances held", got, source, wantInstanceOver)
	}
}

// An instance a load refused keeps its identity until a clean load no longer
// has it.
func TestARefusedInstanceKeepsItsIdentityUntilACleanLoadDropsIt(t *testing.T) {
	other := `{"service_instance_id":9,"bk_host_id":720002,"ip":"192.0.2.172","bk_cloud_id":0}`
	c := newIdentityCase(t, `{"bk_target_service_instance_id":"7","bk_target_ip":"198.51.100.9"}`)
	c.refresh(instanceLoad([]string{"7", instanceSeven, "9", other}, c.now, 0))
	c.refresh(instanceLoad([]string{"9", other}, c.now, 1))
	if got, _ := c.overlaid(); got != wantInstanceOver {
		t.Fatalf("instance refused in one load: overlay %s, want %s", got, wantInstanceOver)
	}
	c.refresh(instanceLoad([]string{"9", other}, c.now, 0))
	if got, _ := c.overlaid(); got != "null" {
		t.Fatalf("instance absent from a clean load: overlay %s, want none", got)
	}
}

// An instance without an address - its host not in the host cache when it
// was built - leaves the record's own address and is named; a record whose
// host its id places is not overlaid by its instance, which Python's fuller
// never reaches.
func TestAnInstanceWithoutAddressAndAHostPlacedRecordAreLeftToTheirOwn(t *testing.T) {
	c := newIdentityCase(t, `{"bk_target_service_instance_id":"8","bk_target_ip":"198.51.100.9"}`)
	c.refresh(instanceLoad([]string{"8", instanceNoIP}, c.now, 0))
	if got, source := c.overlaid(); got != "null" || source != admission.HostIdentityInstanceWithoutAddress {
		t.Fatalf("instance without address: overlay %s (source %q), want none, named %s", got, source, admission.HostIdentityInstanceWithoutAddress)
	}
	// From a stale index the series is counted as its facts unavailable
	// already, and not named a second time.
	c.now = c.now.Add(16 * time.Minute)
	if got, source := c.overlaid(); got != "null" || source != "" {
		t.Fatalf("instance without address, stale index: overlay %s (source %q), want none and unnamed", got, source)
	}
	byID := newIdentityCase(t, `{"bk_host_id":"720002","bk_target_service_instance_id":"7","bk_target_ip":"192.0.2.172"}`)
	byID.refresh(instanceLoad([]string{"7", `{"service_instance_id":7,"bk_host_id":720003,"ip":"192.0.2.173","bk_cloud_id":0}`}, byID.now, 0))
	if got, _ := byID.overlaid(); got != "null" {
		t.Fatalf("host placed by its id: overlay %s, want none from its instance", got)
	}
}

// With the index stale, a record whose host its id places in the identity
// index carries the host's address, not its instance's: Python's fuller
// returns on the host before it reaches the instance.
func TestOnAStaleIndexTheHostByIDWinsOverTheInstance(t *testing.T) {
	c := newIdentityCase(t, `{"bk_host_id":"720002","bk_target_service_instance_id":"7","bk_target_ip":"198.51.100.9"}`)
	published := c.now
	c.refresh(instanceLoad([]string{"7", `{"service_instance_id":7,"bk_host_id":720003,"ip":"192.0.2.173","bk_cloud_id":0}`}, published, 0))
	c.now = published.Add(16 * time.Minute)
	if got, _ := c.overlaid(); got != wantLiveOverlayByID {
		t.Fatalf("stale index, host by id and an instance elsewhere: overlay %s, want the host's %s", got, wantLiveOverlayByID)
	}
}

// A host a load refused is still placed by its id from the identity, and its
// record's instance, which this load does hold, does not overlay it.
func TestARefusedHostByIDWinsOverItsInstance(t *testing.T) {
	c := newIdentityCase(t, `{"bk_host_id":"720002","bk_target_service_instance_id":"7","bk_target_ip":"198.51.100.9"}`)
	elsewhere := `{"service_instance_id":7,"bk_host_id":720003,"ip":"192.0.2.173","bk_cloud_id":0}`
	c.refresh(func() (*Index, error) {
		builder := newIndexBuilder(c.now)
		builder.addFields([]string{"720002", identityLive, "720003", identityOther})
		builder.addServiceInstanceFields([]string{"7", elsewhere})
		builder.index.byAgent, builder.index.publishedAt = map[string]string{}, c.now
		return builder.index, nil
	})
	c.refresh(func() (*Index, error) {
		builder := newIndexBuilder(c.now)
		builder.addFields([]string{"720003", identityOther})
		builder.addServiceInstanceFields([]string{"7", elsewhere})
		builder.index.byAgent, builder.index.publishedAt = map[string]string{}, c.now
		builder.index.refused.Hosts++
		return builder.index, nil
	})
	if got, _ := c.overlaid(); got != wantLiveOverlayByID {
		t.Fatalf("host refused but carried, instance elsewhere: overlay %s, want the host's %s", got, wantLiveOverlayByID)
	}
}
