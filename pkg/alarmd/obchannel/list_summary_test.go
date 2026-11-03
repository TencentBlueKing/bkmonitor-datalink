// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package obchannel

import (
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/sre/pod"
)

// A list page's summary says whether objects are past it, and how many when
// the apiserver says, so a first page is not taken for the whole list.
func TestAListPageSaysWhatIsPastIt(t *testing.T) {
	remaining := int64(1250)
	for _, c := range []struct {
		page pod.ResourceReceipt
		want []string
	}{
		{pod.ResourceReceipt{Count: 500, Continue: "token", RemainingItems: &remaining}, []string{"本页 500 个", "还有 1250 个、约 3 页"}},
		{pod.ResourceReceipt{Count: 500, Continue: "token"}, []string{"本页 500 个", "还有更多页"}},
		{pod.ResourceReceipt{Count: 40, Truncated: true, ItemsOmitted: 10}, []string{"漏了 10 个"}},
		{pod.ResourceReceipt{Count: 12}, []string{"共 12 个对象，已全部列出"}},
	} {
		got := listSummary(c.page)
		for _, want := range c.want {
			if !strings.Contains(got, want) {
				t.Errorf("summary %q, want it to say %q", got, want)
			}
		}
	}
}
