package cache

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/gin-gonic/gin"
	"github.com/spaolacci/murmur3"
)

const (
	// dataKeyType 缓存数据键类型，用于存储实际的数据内容
	// 格式: dsg:data:{cache_key}
	dataKeyType = "data_key"

	// indexKeyType LRU 索引键类型，用于维护缓存项的时间戳排序
	// 格式: dsg:sys:index (全局唯一)
	indexKeyType = "index_key"

	// limitKeyType 缓存限制配置键类型，用于动态设置缓存容量限制
	// 格式: dsg:conf:limit (全局唯一)
	limitKeyType = "limit_key"

	// lockKeyType 分布式锁键类型
	// 格式: dsg:lock:{cache_key}
	lockKeyType = "lock_key"

	// channelKeyType 通知频道键类型
	// 格式: dsg:chan:{cache_key}
	channelKeyType = "channel_key"
)

var (
	trans = map[string]string{
		dataKeyType:    `dsg:data:%s`,
		indexKeyType:   `dsg:sys:index`,
		limitKeyType:   `dsg:conf:limit`,
		lockKeyType:    `dsg:lock:%s`,
		channelKeyType: `dsg:chan:%s`,
	}
)

func generateCacheKey(c *gin.Context) (string, error) {
	ctx := c.Request.Context()
	user := metadata.GetUser(ctx)
	payload := CachePayload{
		req:     c.Request,
		spaceID: user.SpaceUID,
		path:    c.Request.URL.Path,
	}

	pStr, err := json.Marshal(payload)
	return hash(pStr), err
}

func hash(key []byte) string {
	hasher := murmur3.New128()
	result := hasher.Sum(key)
	return string(result)
}

func cacheKeyMap(key string, subject string) string {
	if format, ok := trans[key]; ok {
		return fmt.Sprintf(format, subject)
	}
	return ""
}

func subscribeAll() string {
	channelFormat := trans[channelKeyType]
	return fmt.Sprintf(channelFormat, "*")
}

func extractKeyFromChannel(channel string) string {
	// 1. 使用现有的channelKey格式获取前缀
	channelPrefix := cacheKeyMap(channelKeyType, "")
	if channelPrefix == "" {
		return ""
	}

	// 2. 检查频道前缀
	if !strings.HasPrefix(channel, channelPrefix) {
		return ""
	}

	// 3. 提取key部分
	key := strings.TrimPrefix(channel, channelPrefix)
	if key == "" {
		return ""
	}

	return key
}
