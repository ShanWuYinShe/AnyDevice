package audio

import "time"

// 发送端 Opus 编码参数自适应（采集端）。
//
// 设计目标：与游戏等高负载程序共存——
//   - 高负载时降档（降复杂度/码率）：减少编码 CPU 占用，让编码队列不积压，
//     保证音频时间轴（20ms/帧）不被拉长；
//   - 低负载持续稳定时升档（升码率/复杂度）：改善听感。
//
// 约束（与采集链路其它改造保持一致）：
//   - 只调 Opus 编码参数，不改帧长（保持 20ms）、不改采集设备周期；
//   - 控制器只在 encodeLoop 的编码 goroutine 内运行，不新增常驻 goroutine
//     或定时器（按需启停会反复 Start/Stop，常驻对象容易在每次运行泄漏）；
//   - 决策函数 decideAdaptive 是纯函数，便于表驱动单测；
//   - 切换时的 SetBitrate/SetComplexity 必须与 Feed 串行（由 Engine.opusMu
//     保证），libopus 编码器不是并发安全的。
//
// 档位（bitrate 单位 bit/s）：
//   LevelLow      96000  / complexity 3：高负载保护，省 CPU；
//   LevelBalanced 128000 / complexity 5：默认（NewOpusEncoder 初始值）；
//   LevelQuality  160000 / complexity 7：音质优先。
//
// 切换策略（迟滞，防抖动）：
//   - 降档（保护优先于升档）：queueDepth >= 4 或 droppedDelta > 0 立即降档；
//     encodeMs >= 35ms 需连续 2 次采样（单次尖刺可能只是瞬时调度抖动）；
//   - 升档：encodeMs <= 28ms 且 queueDepth == 0 且 droppedDelta == 0
//     持续 >= 15s 才升档（慢升，避免刚降档又立刻升回去）；
//   - 任意一次切换后至少 8s 不再切换；
//   - 采样间隔 2s：降档最迟约 2s 后生效，远小于接收端缓冲被击穿的量级。

// Level 是 Opus 编码参数自适应档位（数值越大音质越高、CPU 占用越高）。
type Level int

const (
	// LevelLow 高负载保护档：降低码率与复杂度，优先保证编码不积压。
	LevelLow Level = iota
	// LevelBalanced 默认平衡档（128kbps / complexity 5）。
	LevelBalanced
	// LevelQuality 音质优先档：低负载持续稳定时提升到该档。
	LevelQuality
)

// 默认编码参数（平衡档），同时是 NewOpusEncoder 的初始参数。
const (
	defaultOpusBitrate    = 128000
	defaultOpusComplexity = 5
)

// 自适应阈值与采样参数。
const (
	adaptiveSampleInterval = 2 * time.Second  // encodeLoop 内的采样间隔
	adaptiveEncodeHighMs   = int64(35)        // 编码延迟走高阈值（降档）
	adaptiveEncodeLowMs    = int64(28)        // 编码延迟走低阈值（升档）
	adaptiveQueueDepthHigh = 4                // 编码队列积压阈值（降档）
	adaptiveSwitchCooldown = 8 * time.Second  // 任意切换后的冷却时间
	adaptiveUpgradeStable  = 15 * time.Second // 升档所需的低负载持续时长
)

// adaptiveNeverSwitched 表示「尚未发生过切换」：此时冷却期视为已满足
// （取远大于冷却时间的值，避免为「从未切换」再增加一个 bool 信号）。
const adaptiveNeverSwitched = time.Duration(1 << 62)

// params 返回档位对应的 (bitrate bit/s, complexity)；非法档位按平衡档处理。
func (l Level) params() (bitrate, complexity int) {
	switch l {
	case LevelLow:
		return 96000, 3
	case LevelQuality:
		return 160000, 7
	default:
		return defaultOpusBitrate, defaultOpusComplexity
	}
}

// String 返回中文档位名（日志用）。
func (l Level) String() string {
	switch l {
	case LevelLow:
		return "省CPU"
	case LevelQuality:
		return "音质优先"
	default:
		return "平衡"
	}
}

// clamp 把非法档位收敛到 [LevelLow, LevelQuality]，避免档位越界导致参数
// 索引错乱（int 来自 atomic/JSON 等宽类型时可能被写入异常值）。
func (l Level) clamp() Level {
	if l < LevelLow {
		return LevelLow
	}
	if l > LevelQuality {
		return LevelQuality
	}
	return l
}

// adaptiveSignals 是一次采样时自适应决策所需的全部信号。
type adaptiveSignals struct {
	// encodeMs 采集回调 → 编码完成的 EWMA（ms），正常约 20-30ms（含 20ms 组帧）。
	encodeMs int64
	// encodeHighCount encodeMs >= adaptiveEncodeHighMs 的连续采样次数（含本次），
	// 由自适应控制器按 2s 采样节拍维护；需连续 2 次才确认才能降档。
	encodeHighCount int
	// queueDepth 采样时待编码队列积压帧数。
	queueDepth int
	// droppedDelta 距上次采样的编码队列丢帧数。
	droppedDelta uint64
	// sinceLastSwitch 距上次档位切换的时长；从未切换过时应传
	// adaptiveNeverSwitched（>= adaptiveSwitchCooldown）。
	sinceLastSwitch time.Duration
	// stableSince 低负载条件（encodeMs <= adaptiveEncodeLowMs 且无积压、无丢帧）
	// 连续满足的时长；0 表示当前不满足（计时需重新开始）。
	stableSince time.Duration
}

// decideAdaptive 是纯函数形式的档位决策：返回目标档位与是否需要切换。
//
// 判定顺序（保护优先）：冷却期 → 降档 → 升档 → 保持。
// 只依赖入参，不读写任何全局/引擎状态，便于表驱动单测覆盖阈值与边界。
func decideAdaptive(cur Level, sig adaptiveSignals) (Level, bool) {
	cur = cur.clamp()

	// 切换冷却：任意切换后至少 8s 不再切换，避免高负载抖动导致参数反复横跳。
	// 尚未切换过时 sinceLastSwitch 为 adaptiveNeverSwitched，不会命中。
	if sig.sinceLastSwitch < adaptiveSwitchCooldown {
		return cur, false
	}

	// 降档（保护优先）：编码队列积压/丢帧说明已经影响时延，立即降档；
	// 编码延迟偏高需连续 2 次采样确认，避免单帧调度抖动造成误降。
	if cur > LevelLow {
		urgent := sig.queueDepth >= adaptiveQueueDepthHigh || sig.droppedDelta > 0
		encodeSlow := sig.encodeMs >= adaptiveEncodeHighMs && sig.encodeHighCount >= 2
		if urgent || encodeSlow {
			return cur - 1, true
		}
	}

	// 升档：低负载条件连续保持 >= 15s 才信任（慢升）。
	if cur < LevelQuality &&
		sig.encodeMs <= adaptiveEncodeLowMs &&
		sig.queueDepth == 0 &&
		sig.droppedDelta == 0 &&
		sig.stableSince >= adaptiveUpgradeStable {
		return cur + 1, true
	}
	return cur, false
}

// adaptiveState 是自适应控制器的运行状态（只在 encodeLoop 的编码 goroutine
// 内使用，无需加锁；按每次 Start 新建，避免按需启停时与下一次运行串状态）。
//
// 用法：每 adaptiveSampleInterval 调一次 sample 得到决策；参数在持有
// Engine.opusMu 时应用成功后调 commit 提交；应用失败则不 commit，
// 下一个采样会基于旧档位重新判定（等同于本次未切换）。
type adaptiveState struct {
	level       Level
	lastSwitch  time.Time // 零值 = 尚未切换过
	lastDropped uint64
	droppedInit bool
	encodeHigh  int       // encodeMs 连续超阈值的采样次数
	stableSince time.Time // 零值 = 当前不满足低负载条件
}

// newAdaptiveState 返回处于默认平衡档的控制器。
func newAdaptiveState() *adaptiveState {
	return &adaptiveState{level: LevelBalanced}
}

// seedDropped 以当前累计丢帧数建立基线：由 encodeLoop 在每次运行开始时调用，
// 使本次运行前 2s（首个采样点之前）发生的丢帧也能在首个采样中被识别；
// 同时把上一次运行遗留的累计值排除在外，避免跨 Start 误降档。
func (c *adaptiveState) seedDropped(dropped uint64) {
	c.lastDropped = dropped
	c.droppedInit = true
}

// sample 更新统计信号并返回目标档位与是否建议切换（不提交档位）。
// now 由调用方传入，便于单测模拟采样节拍。
func (c *adaptiveState) sample(now time.Time, encodeMs int64, queueDepth int, dropped uint64) (Level, bool) {
	// 丢帧增量：首次采样只建立基线（跨 Start 的累计值不能算作本次丢帧）。
	var droppedDelta uint64
	if c.droppedInit && dropped >= c.lastDropped {
		droppedDelta = dropped - c.lastDropped
	}
	c.lastDropped = dropped
	c.droppedInit = true

	if encodeMs >= adaptiveEncodeHighMs {
		c.encodeHigh++
	} else {
		c.encodeHigh = 0
	}

	benign := encodeMs <= adaptiveEncodeLowMs && queueDepth == 0 && droppedDelta == 0
	if benign {
		if c.stableSince.IsZero() {
			c.stableSince = now
		}
	} else {
		c.stableSince = time.Time{}
	}
	var stableDur time.Duration
	if !c.stableSince.IsZero() {
		stableDur = now.Sub(c.stableSince)
	}

	sinceLastSwitch := adaptiveNeverSwitched
	if !c.lastSwitch.IsZero() {
		sinceLastSwitch = now.Sub(c.lastSwitch)
	}

	return decideAdaptive(c.level, adaptiveSignals{
		encodeMs:        encodeMs,
		encodeHighCount: c.encodeHigh,
		queueDepth:      queueDepth,
		droppedDelta:    droppedDelta,
		sinceLastSwitch: sinceLastSwitch,
		stableSince:     stableDur,
	})
}

// commit 提交一次成功切换：记录新档位、切换时刻并清空确认计数
// （新档位下重新累计，避免降档后立刻按旧计数再次降档）。
func (c *adaptiveState) commit(next Level, now time.Time) {
	c.level = next
	c.lastSwitch = now
	c.encodeHigh = 0
	c.stableSince = time.Time{}
}
