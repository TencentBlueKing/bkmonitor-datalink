package v1beta3

import (
	"context"
	"errors"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
)

func TestTimeGraphNodeInfoLimitBoundaries(t *testing.T) {
	tests := []struct {
		name       string
		limit      int
		timestamps []int64
		wantErr    bool
	}{
		{name: "limit_minus_one", limit: 2, timestamps: []int64{100}, wantErr: false},
		{name: "limit", limit: 2, timestamps: []int64{100, 200}, wantErr: false},
		{name: "limit_plus_one", limit: 2, timestamps: []int64{100, 200, 300}, wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tg := NewTimeGraphWithConfig(&TimeGraphConfig{
				Resource:     []TimeGraphResourceConfig{{Name: "node", Index: cmdb.Index{"id"}}},
				MaxNodeInfos: tc.limit,
			})
			ctx := context.Background()
			for i, timestamp := range tc.timestamps {
				err := tg.AddTimeNode(ctx, "node", cmdb.Matcher{"id": "n1"}, timestamp)
				if tc.wantErr && i == len(tc.timestamps)-1 {
					var limitErr *ResultLimitError
					if !errors.As(err, &limitErr) || limitErr.Reason != "max_graph_node_infos" || limitErr.Limit != tc.limit || limitErr.Count != tc.limit+1 {
						t.Fatalf("unexpected limit error: %v", err)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestTimeGraphCancelledRequestStopsMutation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	tg := NewTimeGraphWithConfig(&TimeGraphConfig{Resource: []TimeGraphResourceConfig{{Name: "node", Index: cmdb.Index{"id"}}}})
	if err := tg.AddTimeNode(ctx, "node", cmdb.Matcher{"id": "n1"}, 100); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancelled add to stop immediately, got %v", err)
	}
	if _, err := tg.FindRelationPathResources(ctx, "node", []cmdb.Resource{"node"}, nil, []cmdb.RelationPath{{Steps: []cmdb.RelationPathStep{{ResourceType: "node"}}}}); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancelled find to stop immediately, got %v", err)
	}
}
