package agent

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

// SystemClock 是生产环境使用的系统 UTC 时间源。
type SystemClock struct{}

// Now 返回当前 UTC 时间，确保跨进程和跨时区的事件时间一致。
func (SystemClock) Now() time.Time { return time.Now().UTC() }

// RandomIDGenerator 使用密码学安全随机数生成内部唯一标识。
type RandomIDGenerator struct{}

// NewID 生成带业务前缀的随机 ID，便于日志和调试时识别对象类型。
func (RandomIDGenerator) NewID(prefix string) (string, error) {
	var value [12]byte
	// 随机源读取失败时不能退化为可碰撞 ID，必须把错误交给调用方处理。
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("generate id: %w", err)
	}
	return prefix + "-" + hex.EncodeToString(value[:]), nil
}
