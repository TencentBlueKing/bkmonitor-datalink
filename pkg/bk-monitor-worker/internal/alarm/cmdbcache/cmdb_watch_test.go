// MIT License

// Copyright (c) 2021~2022 腾讯蓝鲸

// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:

// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.

// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

package cmdbcache

import (
	"testing"
)

func TestResourceWatch(t *testing.T) {
	//redisOptions := redis.Options{
	//	Mode:  "standalone",
	//	Addrs: []string{"127.0.0.1:6379"},
	//}
	//
	//// 系统信号
	//signalChan := make(chan os.Signal, 1)
	//signal.Notify(signalChan, os.Interrupt, os.Kill)
	//
	////调用cancel函数取消
	//ctx, cancel := context.WithCancel(context.Background())
	//defer cancel()
	//
	//// 监听信号
	//go func() {
	//	<-signalChan
	//	cancel()
	//}()
	//
	//prefix := t.Name()
	//
	//wg := &sync.WaitGroup{}
	//wg.Add(2)
	//
	//go func() {
	//	defer cancel()
	//	defer wg.Done()
	//
	//	params := &WatchCmdbResourceChangeEventTaskParams{
	//		Redis:  redisOptions,
	//		Prefix: prefix,
	//	}
	//	payload, _ := json.Marshal(params)
	//	if err := WatchCmdbResourceChangeEventTask(ctx, payload); err != nil {
	//		t.Errorf("TestWatch failed, err: %v", err)
	//		return
	//	}
	//}()
	//
	//go func() {
	//	defer cancel()
	//	defer wg.Done()
	//
	//	params := &RefreshTaskParams{
	//		Redis:                redisOptions,
	//		Prefix:               prefix,
	//		EventHandleInterval:  60,
	//		FullRefreshIntervals: map[string]int{"host_topo": 1800, "business": 1800, "module": 1800, "set": 1800},
	//	}
	//	payload, _ := json.Marshal(params)
	//	if err := CacheRefreshTask(ctx, payload); err != nil {
	//		t.Errorf("TestHandle failed, err: %v", err)
	//		return
	//	}
	//}()
	//
	//wg.Wait()
}
