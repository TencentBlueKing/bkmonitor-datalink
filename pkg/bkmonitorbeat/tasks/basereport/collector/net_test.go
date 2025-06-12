package collector

import (
	"github.com/shirou/gopsutil/v3/net"
	"testing"
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
	if !fifo.CheckMonotonicIncrease() {
		t.Errorf("Expected true, got false")
	}

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
	if fifo.CheckMonotonicIncrease() {
		t.Errorf("Expected false, got true")
	}

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
	if fifo.CheckMonotonicIncrease() {
		t.Errorf("Expected false, got truue")
	}

}
