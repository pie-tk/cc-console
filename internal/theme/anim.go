package theme

import (
	"math"
	"sync"
	"time"
)

// 动效系统：pulseFactor() 用于 busy 状态呼吸值（0..1 正弦）。
// 在 Wails 版本中，脉冲动画由 CSS @keyframes 实现，
// 此处仅保留 animState 供 Go 端了解动画状态（如有需要）。

const pulsePeriod = 1600 * time.Millisecond

var anim = &animState{
	startedAt: time.Now(),
}

type animState struct {
	mu        sync.RWMutex
	startedAt time.Time
}

// PulseFactor 返回 0..1 的正弦相位值，~1.6s 一周期。
func PulseFactor() float64 {
	anim.mu.RLock()
	elapsed := time.Since(anim.startedAt)
	anim.mu.RUnlock()
	x := math.Sin(2 * math.Pi * float64(elapsed) / float64(pulsePeriod))
	return 0.55 + 0.45*x
}
