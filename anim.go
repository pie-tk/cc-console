package main

import (
	"math"
	"sync"
	"time"

	"github.com/lxn/walk"
)

// 动效系统：80ms tick 驱动
//   - pulseFactor()：busy 状态文字 / 心跳灯的呼吸值（0..1 正弦）
//   - 底部反馈消息的 TTL 淡出
//
// 所有方法可被任意 goroutine 调用，GUI 线程通过 mw.Synchronize 读取。

const (
	pulsePeriod    = 1600 * time.Millisecond
	footMsgFadeDur = 4500 * time.Millisecond
)

type animState struct {
	mu sync.RWMutex

	startedAt time.Time

	footMsg   string
	footMark  time.Time
	footFresh bool
}

var anim = &animState{
	startedAt: time.Now(),
}

// pulseFactor 返回 0..1 的正弦相位值，~1.6s 一周期。
// 用于 busy 状态色：blendColor(dim, bright, pulseFactor())
func pulseFactor() float64 {
	anim.mu.RLock()
	elapsed := time.Since(anim.startedAt)
	anim.mu.RUnlock()
	x := math.Sin(2 * math.Pi * float64(elapsed) / float64(pulsePeriod))
	return 0.55 + 0.45*x
}

// ---- 底部消息 ----

// setFootMessage 设置带 TTL 的反馈消息。
func setFootMessage(s string) {
	anim.mu.Lock()
	anim.footMsg = s
	anim.footMark = time.Now()
	anim.footFresh = true
	anim.mu.Unlock()
}

// footMessage 返回当前应显示的消息及其衰减进度（0..1，>=1 表示已过期）。
func footMessage() (string, float64) {
	anim.mu.RLock()
	defer anim.mu.RUnlock()
	if !anim.footFresh {
		return "", 1
	}
	elapsed := time.Since(anim.footMark)
	if elapsed >= footMsgFadeDur {
		return "", 1
	}
	return anim.footMsg, float64(elapsed) / float64(footMsgFadeDur)
}

// clearFootIfStale 把过期消息标记清除。
func clearFootIfStale() {
	anim.mu.Lock()
	if anim.footFresh && time.Since(anim.footMark) >= footMsgFadeDur {
		anim.footFresh = false
	}
	anim.mu.Unlock()
}

// ---- 动画驱动 ----
//
// 启动 80ms ticker，每次让 GUI 线程跑 onTick。
// 仅在存在 busy 实例或有未过期底部消息时才执行 GUI 更新，
// 空闲时跳过，减少无效 GUI 调度。
//
// 第二个参数（曾用于 TableView Invalidate）已废弃，保留 nil 以兼容调用方。
func startAnimationLoop(mw *walk.MainWindow, _ interface{}, onTick func()) {
	go func() {
		t := time.NewTicker(80 * time.Millisecond)
		defer t.Stop()
		for range t.C {
			if !animNeedsTick() {
				continue
			}
			mw.Synchronize(func() {
				if onTick != nil {
					onTick()
				}
			})
		}
	}()
}

// animNeedsTick 判断当前是否需要执行动画 tick。
// 有 busy 卡片或有未过期底部消息时返回 true。
func animNeedsTick() bool {
	for _, c := range activeCards {
		if c != nil && c.emoji != nil && c.emoji.Text() == statusEmoji("busy") {
			return true
		}
	}
	anim.mu.RLock()
	fresh := anim.footFresh
	anim.mu.RUnlock()
	return fresh
}
