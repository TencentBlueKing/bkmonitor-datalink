package middleware

import (
	"bou.ke/monkey"
	"github.com/smartystreets/goconvey/convey"
	"log"
	"testing"
)

func TestSingleGetInstance(t *testing.T) {
	convey.Convey("测试获取实例IP次数是否为单次", t, func() {
		tests := []struct {
			name    string
			mockIPs []string
			want    string
		}{
			// TODO: Add test cases.
			{
				"多次调用singleGetInstance函数",
				[]string{"192.168.0.1", "192.168.0.2", "192.168.0.3"},
				"192.168.0.1",
			},
		}

		for _, tt := range tests {
			convey.Convey(tt.name, func() {
				for _, mockIP := range tt.mockIPs {
					monkey.Patch(getInstanceip, func() (string, error) {
						return mockIP, nil
					})
					got := singleGetInstance()
					log.Println("mockIP:", mockIP)
					log.Println("got:", got)
					convey.So(got, convey.ShouldContainSubstring, tt.want)
				}
			})
		}
	})
}
