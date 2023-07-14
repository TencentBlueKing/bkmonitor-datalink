package metadata

import (
	"context"
	"github.com/smartystreets/goconvey/convey"
	"log"
	"testing"
)

func TestCheckVmQuery(t *testing.T) {
	tt := []struct {
		name string
		//expect []string
		mockctx            context.Context
		mockvmQuery        bool
		mockVmRt           string
		mockIsSingleMetric bool
		mockDimensions     []string
		want               string
	}{
		{
			"测试durid查询下vmrt的替换",
			context.Background(),
			true,
			"mockvmrt_100147_ieod_system_net_raw",
			true,
			[]string{"bk_inst_id", "bk_obj_id"},
			"_cmdb",
		},
		{
			"测试非durid查询下是否会替换vmrt内容",
			context.Background(),
			true,
			"mockvmrt_100147_ieod_system_net_raw",
			true,
			[]string{"bk_mock1_id", "bk_mock2_id"},
			"_raw",
		},
		{
			"测试非durid查询下，检查到单个拆分表默认维度bk_obj_id时是否会替换vmrt内容",
			context.Background(),
			true,
			"mockvmrt_100147_ieod_system_net_raw",
			true,
			[]string{"bk_mock1_id", "bk_obj_id"},
			"_raw",
		},
		{
			"测试非durid查询下，检查到单个拆分表默认维度bk_obj_id时是否会替换vmrt内容",
			context.Background(),
			true,
			"mockvmrt_100147_ieod_system_net_raw",
			true,
			[]string{"bk_inst_id", "bk_mock2_id"},
			"_raw",
		},
	}

	for _, tc := range tt {
		convey.Convey(tc.name, t, func() {
			mockAggrMethod := AggrMethod{Dimensions: tc.mockDimensions}
			mockQuery := Query{VmRt: tc.mockVmRt, IsSingleMetric: tc.mockIsSingleMetric, AggregateMethodList: []AggrMethod{mockAggrMethod}}
			mockQueryList := []*Query{&mockQuery}
			mockQueryMetric := QueryMetric{QueryList: mockQueryList}
			mockQueryReference := QueryReference{"mockReferenceName": &mockQueryMetric}
			log.Println("模拟变量赋值完成，准备调用CheckVmQuery")

			_, _, vmRtGroup, _ := mockQueryReference.CheckVmQuery(tc.mockctx, tc.mockvmQuery)
			log.Println("获取vmRtGroup完成")
			log.Println("vmRtGroup:", vmRtGroup)
			for _, vmrts := range vmRtGroup {
				for _, vmrt := range vmrts {
					convey.So(vmrt, convey.ShouldContainSubstring, tc.want)
					log.Println("convey assert done")
				}
			}
		})
	}

}
