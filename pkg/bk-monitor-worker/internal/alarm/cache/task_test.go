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

package cache

// 正式运行测试
//func TestAlarmCacheTask(t *testing.T) {
//	// 业务缓存
//	rOpts := redis.RedisOptions{
//		Mode:  "standalone",
//		Addrs: []string{"127.0.0.1:6379"},
//	}
//
//	ctx := context.Background()
//	params, err := json.Marshal(RefreshHostAndTopoCacheByBizParams{
//		Redis:          rOpts,
//		CacheKeyPrefix: t.Name(),
//		Type:           "module",
//	})
//	if err != nil {
//		t.Fatal(err)
//	}
//
//	tt := &task.Task{
//		Kind:    "alarm_cache",
//		Payload: params,
//	}
//
//	err = RefreshAlarmCacheTask(ctx, tt)
//	if err != nil {
//		t.Fatal(err)
//	}
//}
