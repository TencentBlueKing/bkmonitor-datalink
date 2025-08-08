package collector

import (
	"regexp"
	"testing"
	"time"

	"github.com/shirou/gopsutil/v3/net"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
)

func TestFIFOQueue(t *testing.T) {
	// 测试用例 1：单调递增
	fifo := NewNetFIFOQueue(4)
	fifo.Push([]Stat{
		{
			IOCountersStat: net.IOCountersStat{Name: "eth0", BytesSent: 100, BytesRecv: 200, PacketsSent: 10, PacketsRecv: 20},
		},
		{
			IOCountersStat: net.IOCountersStat{Name: "eth1", BytesSent: 50, BytesRecv: 100, PacketsSent: 5, PacketsRecv: 10},
		},
	})
	fifo.Push([]Stat{
		{
			IOCountersStat: net.IOCountersStat{Name: "eth0", BytesSent: 150, BytesRecv: 250, PacketsSent: 15, PacketsRecv: 25},
		},
		{
			IOCountersStat: net.IOCountersStat{Name: "eth1", BytesSent: 100, BytesRecv: 200, PacketsSent: 10, PacketsRecv: 20},
		},
	})
	fifo.Push([]Stat{
		{
			IOCountersStat: net.IOCountersStat{Name: "eth0", BytesSent: 200, BytesRecv: 300, PacketsSent: 20, PacketsRecv: 30},
		},
		{
			IOCountersStat: net.IOCountersStat{Name: "eth1", BytesSent: 150, BytesRecv: 300, PacketsSent: 15, PacketsRecv: 30},
		},
	})
	fifo.Push([]Stat{
		{
			IOCountersStat: net.IOCountersStat{Name: "eth0", BytesSent: 250, BytesRecv: 350, PacketsSent: 25, PacketsRecv: 35},
		},
		{
			IOCountersStat: net.IOCountersStat{Name: "eth1", BytesSent: 200, BytesRecv: 400, PacketsSent: 20, PacketsRecv: 40},
		},
	})
	assert.True(t, fifo.CheckMonotonicIncrease())

	// 测试用例 2：不单调递增
	fifo = NewNetFIFOQueue(4)
	fifo.Push([]Stat{
		{
			IOCountersStat: net.IOCountersStat{Name: "eth0", BytesSent: 100, BytesRecv: 200, PacketsSent: 10, PacketsRecv: 20},
		},
		{
			IOCountersStat: net.IOCountersStat{Name: "eth1", BytesSent: 50, BytesRecv: 100, PacketsSent: 5, PacketsRecv: 10},
		},
	})
	fifo.Push([]Stat{
		{
			IOCountersStat: net.IOCountersStat{Name: "eth0", BytesSent: 150, BytesRecv: 250, PacketsSent: 15, PacketsRecv: 25},
		},
		{
			IOCountersStat: net.IOCountersStat{Name: "eth1", BytesSent: 45, BytesRecv: 200, PacketsSent: 10, PacketsRecv: 20},
		}, // BytesSent 不单调递增
	})
	fifo.Push([]Stat{
		{
			IOCountersStat: net.IOCountersStat{Name: "eth0", BytesSent: 200, BytesRecv: 300, PacketsSent: 20, PacketsRecv: 30},
		},
		{
			IOCountersStat: net.IOCountersStat{Name: "eth1", BytesSent: 150, BytesRecv: 300, PacketsSent: 15, PacketsRecv: 30},
		},
	})
	fifo.Push([]Stat{
		{
			IOCountersStat: net.IOCountersStat{Name: "eth0", BytesSent: 250, BytesRecv: 350, PacketsSent: 25, PacketsRecv: 35},
		},
		{
			IOCountersStat: net.IOCountersStat{Name: "eth1", BytesSent: 200, BytesRecv: 400, PacketsSent: 20, PacketsRecv: 40},
		},
	})
	assert.False(t, fifo.CheckMonotonicIncrease())

	// 测试用例 3：数据不足
	fifo = NewNetFIFOQueue(4)
	fifo.Push([]Stat{
		{
			IOCountersStat: net.IOCountersStat{Name: "eth0", BytesSent: 100, BytesRecv: 200, PacketsSent: 10, PacketsRecv: 20},
		},
		{
			IOCountersStat: net.IOCountersStat{Name: "eth1", BytesSent: 50, BytesRecv: 100, PacketsSent: 5, PacketsRecv: 10},
		},
	})
	fifo.Push([]Stat{
		{
			IOCountersStat: net.IOCountersStat{Name: "eth0", BytesSent: 150, BytesRecv: 250, PacketsSent: 15, PacketsRecv: 25},
		},
		{
			IOCountersStat: net.IOCountersStat{Name: "eth1", BytesSent: 100, BytesRecv: 200, PacketsSent: 10, PacketsRecv: 20},
		},
	})
	assert.False(t, fifo.CheckMonotonicIncrease())
}

// TestIsNetworkStatsMonotonic 测试新增的 isNetworkStatsMonotonic 辅助函数
func TestIsNetworkStatsMonotonic(t *testing.T) {
	fifo := NewNetFIFOQueue(4)

	// 测试空数据
	stats := []Stat{}
	assert.True(t, fifo.isNetworkStatsMonotonic(stats))

	// 测试单条数据
	stats = []Stat{
		{IOCountersStat: net.IOCountersStat{Name: "eth0", BytesSent: 100, BytesRecv: 200, PacketsSent: 10, PacketsRecv: 20}},
	}
	assert.True(t, fifo.isNetworkStatsMonotonic(stats))

	// 测试正常单调递增
	stats = []Stat{
		{IOCountersStat: net.IOCountersStat{Name: "eth0", BytesSent: 100, BytesRecv: 200, PacketsSent: 10, PacketsRecv: 20}},
		{IOCountersStat: net.IOCountersStat{Name: "eth0", BytesSent: 150, BytesRecv: 250, PacketsSent: 15, PacketsRecv: 25}},
		{IOCountersStat: net.IOCountersStat{Name: "eth0", BytesSent: 200, BytesRecv: 300, PacketsSent: 20, PacketsRecv: 30}},
	}
	assert.True(t, fifo.isNetworkStatsMonotonic(stats))

	// 测试 BytesSent 不单调递增
	stats = []Stat{
		{IOCountersStat: net.IOCountersStat{Name: "eth0", BytesSent: 100, BytesRecv: 200, PacketsSent: 10, PacketsRecv: 20}},
		{IOCountersStat: net.IOCountersStat{Name: "eth0", BytesSent: 90, BytesRecv: 250, PacketsSent: 15, PacketsRecv: 25}},
	}
	assert.False(t, fifo.isNetworkStatsMonotonic(stats))

	// 测试 BytesRecv 不单调递增
	stats = []Stat{
		{IOCountersStat: net.IOCountersStat{Name: "eth0", BytesSent: 100, BytesRecv: 200, PacketsSent: 10, PacketsRecv: 20}},
		{IOCountersStat: net.IOCountersStat{Name: "eth0", BytesSent: 150, BytesRecv: 190, PacketsSent: 15, PacketsRecv: 25}},
	}
	assert.False(t, fifo.isNetworkStatsMonotonic(stats))

	// 测试 PacketsSent 不单调递增
	stats = []Stat{
		{IOCountersStat: net.IOCountersStat{Name: "eth0", BytesSent: 100, BytesRecv: 200, PacketsSent: 10, PacketsRecv: 20}},
		{IOCountersStat: net.IOCountersStat{Name: "eth0", BytesSent: 150, BytesRecv: 250, PacketsSent: 8, PacketsRecv: 25}},
	}
	assert.False(t, fifo.isNetworkStatsMonotonic(stats))

	// 测试 PacketsRecv 不单调递增
	stats = []Stat{
		{IOCountersStat: net.IOCountersStat{Name: "eth0", BytesSent: 100, BytesRecv: 200, PacketsSent: 10, PacketsRecv: 20}},
		{IOCountersStat: net.IOCountersStat{Name: "eth0", BytesSent: 150, BytesRecv: 250, PacketsSent: 15, PacketsRecv: 18}},
	}
	assert.False(t, fifo.isNetworkStatsMonotonic(stats))

	// 测试相等值（不严格递增）
	stats = []Stat{
		{IOCountersStat: net.IOCountersStat{Name: "eth0", BytesSent: 100, BytesRecv: 200, PacketsSent: 10, PacketsRecv: 20}},
		{IOCountersStat: net.IOCountersStat{Name: "eth0", BytesSent: 100, BytesRecv: 250, PacketsSent: 15, PacketsRecv: 25}},
	}
	assert.False(t, fifo.isNetworkStatsMonotonic(stats))
}

// TestFIFOQueuePush 测试队列Push功能的边界情况
func TestFIFOQueuePush(t *testing.T) {
	// 测试队列未满的情况
	fifo := NewNetFIFOQueue(3)
	assert.Equal(t, 0, len(fifo.queue))

	stats1 := []Stat{
		{IOCountersStat: net.IOCountersStat{Name: "eth0", BytesSent: 100}},
	}
	fifo.Push(stats1)
	assert.Equal(t, 1, len(fifo.queue))

	stats2 := []Stat{
		{IOCountersStat: net.IOCountersStat{Name: "eth0", BytesSent: 200}},
	}
	fifo.Push(stats2)
	assert.Equal(t, 2, len(fifo.queue))

	// 测试队列满后的FIFO行为
	stats3 := []Stat{
		{IOCountersStat: net.IOCountersStat{Name: "eth0", BytesSent: 300}},
	}
	fifo.Push(stats3)
	assert.Equal(t, 3, len(fifo.queue))

	stats4 := []Stat{
		{IOCountersStat: net.IOCountersStat{Name: "eth0", BytesSent: 400}},
	}
	fifo.Push(stats4)
	assert.Equal(t, 3, len(fifo.queue)) // 队列长度不变

	// 验证最老的数据被移除，最新的数据被添加
	assert.Equal(t, uint64(200), fifo.queue[0][0].BytesSent) // 原来的stats1被移除
	assert.Equal(t, uint64(400), fifo.queue[2][0].BytesSent) // stats4被添加到末尾
}

// TestCheckMonotonicIncreaseEdgeCases 测试CheckMonotonicIncrease的边界情况
func TestCheckMonotonicIncreaseEdgeCases(t *testing.T) {
	// 测试空队列
	fifo := NewNetFIFOQueue(3)
	assert.False(t, fifo.CheckMonotonicIncrease())

	// 测试单次数据
	fifo.Push([]Stat{
		{IOCountersStat: net.IOCountersStat{Name: "eth0", BytesSent: 100}},
	})
	assert.False(t, fifo.CheckMonotonicIncrease())

	// 测试不同网卡在不同轮次中出现的情况
	fifo = NewNetFIFOQueue(3)
	fifo.Push([]Stat{
		{IOCountersStat: net.IOCountersStat{Name: "eth0", BytesSent: 100, BytesRecv: 200, PacketsSent: 10, PacketsRecv: 20}},
		{IOCountersStat: net.IOCountersStat{Name: "eth1", BytesSent: 50, BytesRecv: 100, PacketsSent: 5, PacketsRecv: 10}},
	})
	fifo.Push([]Stat{
		{IOCountersStat: net.IOCountersStat{Name: "eth0", BytesSent: 150, BytesRecv: 250, PacketsSent: 15, PacketsRecv: 25}},
		// eth1 在这轮中不存在
	})
	fifo.Push([]Stat{
		{IOCountersStat: net.IOCountersStat{Name: "eth0", BytesSent: 200, BytesRecv: 300, PacketsSent: 20, PacketsRecv: 30}},
		{IOCountersStat: net.IOCountersStat{Name: "eth1", BytesSent: 100, BytesRecv: 200, PacketsSent: 10, PacketsRecv: 20}},
		{IOCountersStat: net.IOCountersStat{Name: "eth2", BytesSent: 75, BytesRecv: 150, PacketsSent: 8, PacketsRecv: 15}}, // 新网卡
	})

	// eth0 应该是单调递增的，但 eth1 只有两个数据点，eth2 只有一个数据点
	// 由于 eth1 和 eth2 数据不足，它们会被认为是单调的
	assert.True(t, fifo.CheckMonotonicIncrease())
}

// TestMultipleNetworkInterfaces 测试多网卡混合场景
func TestMultipleNetworkInterfaces(t *testing.T) {
	fifo := NewNetFIFOQueue(3)

	// 第一轮：eth0 和 eth1
	fifo.Push([]Stat{
		{IOCountersStat: net.IOCountersStat{Name: "eth0", BytesSent: 100, BytesRecv: 200, PacketsSent: 10, PacketsRecv: 20}},
		{IOCountersStat: net.IOCountersStat{Name: "eth1", BytesSent: 50, BytesRecv: 100, PacketsSent: 5, PacketsRecv: 10}},
	})

	// 第二轮：eth0, eth1 和新增的 lo
	fifo.Push([]Stat{
		{IOCountersStat: net.IOCountersStat{Name: "eth0", BytesSent: 150, BytesRecv: 250, PacketsSent: 15, PacketsRecv: 25}},
		{IOCountersStat: net.IOCountersStat{Name: "eth1", BytesSent: 100, BytesRecv: 200, PacketsSent: 10, PacketsRecv: 20}},
		{IOCountersStat: net.IOCountersStat{Name: "lo", BytesSent: 10, BytesRecv: 10, PacketsSent: 1, PacketsRecv: 1}},
	})

	// 第三轮：所有网卡都单调递增
	fifo.Push([]Stat{
		{IOCountersStat: net.IOCountersStat{Name: "eth0", BytesSent: 200, BytesRecv: 300, PacketsSent: 20, PacketsRecv: 30}},
		{IOCountersStat: net.IOCountersStat{Name: "eth1", BytesSent: 150, BytesRecv: 300, PacketsSent: 15, PacketsRecv: 30}},
		{IOCountersStat: net.IOCountersStat{Name: "lo", BytesSent: 20, BytesRecv: 20, PacketsSent: 2, PacketsRecv: 2}},
	})

	assert.True(t, fifo.CheckMonotonicIncrease())

	// 添加一轮数据，其中 eth1 出现倒流
	fifo.Push([]Stat{
		{IOCountersStat: net.IOCountersStat{Name: "eth0", BytesSent: 250, BytesRecv: 350, PacketsSent: 25, PacketsRecv: 35}},
		{IOCountersStat: net.IOCountersStat{Name: "eth1", BytesSent: 140, BytesRecv: 350, PacketsSent: 20, PacketsRecv: 40}}, // BytesSent 倒流
		{IOCountersStat: net.IOCountersStat{Name: "lo", BytesSent: 30, BytesRecv: 30, PacketsSent: 3, PacketsRecv: 3}},
	})

	assert.False(t, fifo.CheckMonotonicIncrease())
}

// TestGetNetInfoBasic 测试 GetNetInfo 的基本功能
func TestGetNetInfoBasic(t *testing.T) {
	// 重置全局变量
	resetGlobalNetVars()

	config := configs.NetConfig{
		StatTimes:  1,
		StatPeriod: time.Millisecond * 100,
	}

	report, err := GetNetInfo(config)

	// 基本断言
	assert.NoError(t, err)
	assert.NotNil(t, report)
	assert.NotNil(t, report.Interface)
	assert.NotNil(t, report.Stat)
}

// TestGetNetInfoWithFilters 测试带过滤器的 GetNetInfo
func TestGetNetInfoWithFilters(t *testing.T) {
	resetGlobalNetVars()

	// 创建正则表达式用于测试
	ethRegex, _ := regexp.Compile("eth.*")
	loRegex, _ := regexp.Compile("lo")

	config := configs.NetConfig{
		StatTimes:            1,
		StatPeriod:           time.Millisecond * 100,
		InterfaceWhiteList:   []*regexp.Regexp{ethRegex},
		InterfaceBlackList:   []*regexp.Regexp{loRegex},
		SkipVirtualInterface: false,
	}

	report, err := GetNetInfo(config)

	assert.NoError(t, err)
	assert.NotNil(t, report)
}

// TestGetNetInfoMultipleTimes 测试多次采样
func TestGetNetInfoMultipleTimes(t *testing.T) {
	resetGlobalNetVars()

	config := configs.NetConfig{
		StatTimes:  3,
		StatPeriod: time.Millisecond * 50,
	}

	report, err := GetNetInfo(config)

	assert.NoError(t, err)
	assert.NotNil(t, report)

	// 验证是否收集了多次数据（通过检查速度字段是否被计算）
	if len(report.Stat) > 0 {
		// 由于是多次采样，应该有速度计算
		t.Logf("Collected %d network interfaces", len(report.Stat))
	}
}

// TestGetNetInfoWithVirtualInterfaceSkip 测试跳过虚拟网卡
func TestGetNetInfoWithVirtualInterfaceSkip(t *testing.T) {
	resetGlobalNetVars()

	config := configs.NetConfig{
		StatTimes:            1,
		StatPeriod:           time.Millisecond * 100,
		SkipVirtualInterface: true,
	}

	report, err := GetNetInfo(config)

	assert.NoError(t, err)
	assert.NotNil(t, report)
}

// TestGetNetInfoForceReportList 测试强制上报列表
func TestGetNetInfoForceReportList(t *testing.T) {
	resetGlobalNetVars()

	forceRegex, _ := regexp.Compile(".*") // 匹配所有

	config := configs.NetConfig{
		StatTimes:       1,
		StatPeriod:      time.Millisecond * 100,
		ForceReportList: []*regexp.Regexp{forceRegex},
	}

	report, err := GetNetInfo(config)

	assert.NoError(t, err)
	assert.NotNil(t, report)
}

// TestUpdateNetSpeedWithBackflow 测试网络速度更新时的倒流处理
func TestUpdateNetSpeedWithBackflow(t *testing.T) {
	resetGlobalNetVars()

	// 设置初始状态
	lastNetStatMap = map[string]net.IOCountersStat{
		"eth0": {
			Name:        "eth0",
			BytesSent:   1000,
			BytesRecv:   2000,
			PacketsSent: 100,
			PacketsRecv: 200,
		},
	}

	// 创建倒流数据
	backflowReport := &NetReport{
		Stat: []Stat{
			{
				IOCountersStat: net.IOCountersStat{
					Name:        "eth0",
					BytesSent:   800, // 倒流
					BytesRecv:   2500,
					PacketsSent: 120,
					PacketsRecv: 250,
				},
			},
		},
	}

	// 测试倒流处理
	updateNetSpeed(backflowReport, 1)

	// 验证速度被重置为0
	assert.Equal(t, uint64(0), backflowReport.Stat[0].SpeedSent)
	assert.Equal(t, uint64(0), backflowReport.Stat[0].SpeedRecv)
	assert.Equal(t, uint64(0), backflowReport.Stat[0].SpeedPacketsSent)
	assert.Equal(t, uint64(0), backflowReport.Stat[0].SpeedPacketsRecv)

	// 验证错误计数增加
	assert.Greater(t, errCount, 0)
}

// TestUpdateNetSpeedNormal 测试正常的网络速度更新
func TestUpdateNetSpeedNormal(t *testing.T) {
	resetGlobalNetVars()

	// 设置初始状态
	lastNetStatMap = map[string]net.IOCountersStat{
		"eth0": {
			Name:        "eth0",
			BytesSent:   1000,
			BytesRecv:   2000,
			PacketsSent: 100,
			PacketsRecv: 200,
		},
	}

	// 创建正常递增数据
	normalReport := &NetReport{
		Stat: []Stat{
			{
				IOCountersStat: net.IOCountersStat{
					Name:        "eth0",
					BytesSent:   1500,
					BytesRecv:   2500,
					PacketsSent: 150,
					PacketsRecv: 250,
				},
			},
		},
	}

	// 测试正常更新
	updateNetSpeed(normalReport, 1) // 1秒间隔

	// 验证速度计算正确
	assert.Equal(t, uint64(500), normalReport.Stat[0].SpeedSent)       // (1500-1000)/1
	assert.Equal(t, uint64(500), normalReport.Stat[0].SpeedRecv)       // (2500-2000)/1
	assert.Equal(t, uint64(50), normalReport.Stat[0].SpeedPacketsSent) // (150-100)/1
	assert.Equal(t, uint64(50), normalReport.Stat[0].SpeedPacketsRecv) // (250-200)/1
}

// TestUpdateNetSpeedRecovery 测试网络错误恢复
func TestUpdateNetSpeedRecovery(t *testing.T) {
	resetGlobalNetVars()

	// 设置错误状态
	errCount = 1

	// 创建恢复数据
	recoveryReport := &NetReport{
		Stat: []Stat{
			{
				IOCountersStat: net.IOCountersStat{
					Name:        "eth0",
					BytesSent:   1000,
					BytesRecv:   2000,
					PacketsSent: 100,
					PacketsRecv: 200,
				},
			},
		},
	}

	// 测试恢复处理
	updateNetSpeed(recoveryReport, 1)

	// 验证错误计数被重置
	assert.Equal(t, 0, errCount)

	// 验证速度被重置（恢复时不计算速度）
	assert.Equal(t, uint64(0), recoveryReport.Stat[0].SpeedSent)
	assert.Equal(t, uint64(0), recoveryReport.Stat[0].SpeedRecv)
}

// TestUpdateNetSpeedMonotonicRecovery 测试连续单调递增但值仍小于初始值的恢复场景
func TestUpdateNetSpeedMonotonicRecovery(t *testing.T) {
	resetGlobalNetVars()

	// 设置初始的高值状态
	lastNetStatMap = map[string]net.IOCountersStat{
		"eth0": {
			Name:        "eth0",
			BytesSent:   10000, // 初始值较大
			BytesRecv:   20000,
			PacketsSent: 1000,
			PacketsRecv: 2000,
		},
	}

	// 推送5次单调递增的数据到FIFO队列，模拟连续5次单调递增
	for i := 0; i < 5; i++ {

		currentReport := &NetReport{
			Stat: []Stat{
				{
					IOCountersStat: net.IOCountersStat{
						Name:        "eth0",
						BytesSent:   uint64(100 + i*50), // 单调递增但仍小于初始10000
						BytesRecv:   uint64(200 + i*50),
						PacketsSent: uint64(10 + i*5),
						PacketsRecv: uint64(20 + i*5),
					},
				},
			},
		}
		updateNetSpeed(currentReport, 1)
	}

	// 验证FIFO队列确实认为是单调递增的
	assert.True(t, netStatFIFOQueue.CheckMonotonicIncrease())

	// 创建当前数据，仍然小于初始值但单调递增
	currentReport := &NetReport{
		Stat: []Stat{
			{
				IOCountersStat: net.IOCountersStat{
					Name:        "eth0",
					BytesSent:   350, // 单调递增但远小于初始的10000
					BytesRecv:   450,
					PacketsSent: 35,
					PacketsRecv: 45,
				},
			},
		},
	}

	// 测试这种情况：虽然单调递增，但当前值 < 初始值，仍会检测到倒流
	updateNetSpeed(currentReport, 1)

	// 由于当前值(350) < lastNetStatMap中的值(10000)，会检测到倒流
	// 但由于FIFO队列满足单调递增，hasBackflow && !CheckMonotonicIncrease() 为 false
	// 所以不会进入错误处理，而是正常处理
	// 验证速度被正常计算（这是倒流恢复的情况）
	assert.Equal(t, uint64(50), currentReport.Stat[0].SpeedSent)       // (350-300)/1
	assert.Equal(t, uint64(50), currentReport.Stat[0].SpeedRecv)       // (450-400)/1
	assert.Equal(t, uint64(5), currentReport.Stat[0].SpeedPacketsSent) // (35-30)/1
	assert.Equal(t, uint64(5), currentReport.Stat[0].SpeedPacketsRecv) // (45-40)/1
}

// resetGlobalNetVars 重置全局变量，用于测试隔离
func resetGlobalNetVars() {
	netStatFIFOQueue = NewNetFIFOQueue(5)
	lastNetStatMap = nil
	errCount = 0
	lastStatTime = time.Time{}
}
