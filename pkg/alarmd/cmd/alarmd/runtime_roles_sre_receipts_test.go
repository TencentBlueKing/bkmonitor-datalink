// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/obchannel"
)

// The channel's audit also records each action's receipt, under the
// deployment's SRE prefix, where sre.receipts reads it; without a runtime
// store the operation says it is unavailable.
func TestTheChannelRecordsReceiptsWhereSREReceiptsReadsThem(t *testing.T) {
	_, client := startPhaseTwoRedis(t)
	cfg := config.Default()
	binding := ChannelBinding{EnvironmentID: "env"}
	var heard []obchannel.SREAction
	written := []bool{}
	binding.SREReceiptWritten = func(ok bool) { written = append(written, ok) }
	receipts := obchannel.NewSREReceipts(client, sreKeyPrefix(cfg, binding)+"receipts", nil, binding.SREReceiptWritten)
	audit := auditWithReceipts(func(action obchannel.SREAction) { heard = append(heard, action) }, receipts)
	audit(obchannel.SREAction{Op: obchannel.OpRevert, Namespace: "ns", Kind: "Deployment", Name: "web", RequestID: "req-1", Result: obchannel.SREResultDone})
	if len(heard) != 1 || len(written) != 1 || !written[0] {
		t.Fatalf("heard %d, written %v", len(heard), written)
	}
	operations, closeProvider, err := channelSREOperations(cfg, binding, client)
	if err != nil {
		t.Fatal(err)
	}
	defer closeProvider()
	for _, operation := range operations {
		if operation.ID != obchannel.OpSREReceipts {
			continue
		}
		out := operation.Run(context.Background(), obchannel.Params{"request_id": "req-1"})
		encoded, _ := json.Marshal(out.Value)
		var page obchannel.SREReceiptPage
		if out.Error != nil || json.Unmarshal(encoded, &page) != nil || page.Matched != 1 || page.Receipts[0].Op != obchannel.OpRevert {
			t.Fatalf("read %s %+v", encoded, out.Error)
		}
		none, closeNone, err := channelSREOperations(cfg, binding, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer closeNone()
		for _, operation := range none {
			if operation.ID == obchannel.OpSREReceipts && operation.Availability().Available {
				t.Fatal("sre.receipts is available without a runtime store")
			}
		}
		return
	}
	t.Fatal("sre.receipts is missing")
}
