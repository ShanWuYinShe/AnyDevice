package audio

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// okSignals 返回低负载信号基线（冷却已满足、无积压/丢帧、编码延迟正常），
// 各用例在它之上覆盖需要考察的字段。
func okSignals() adaptiveSignals {
	return adaptiveSignals{
		encodeMs:        24,
		encodeHighCount: 0,
		queueDepth:      0,
		droppedDelta:    0,
		sinceLastSwitch: adaptiveNeverSwitched,
		stableSince:     0, // 默认未满足升档所需时长
	}
}

// TestDecideAdaptive 决策表：覆盖降档（积压/丢帧/编码延迟连续超阈值）、
// 升档（低负载持续时长）、迟滞（冷却期/单次尖刺/边界值）与非法档位。
func TestDecideAdaptive(t *testing.T) {
	cases := []struct {
		name       string
		cur        Level
		mutate     func(*adaptiveSignals)
		want       Level
		wantSwitch bool
	}{
		{
			name: "队列积压4立即降档", cur: LevelBalanced,
			mutate: func(s *adaptiveSignals) { s.queueDepth = 4 },
			want:   LevelLow, wantSwitch: true,
		},
		{
			name: "队列积压3不降档", cur: LevelBalanced,
			mutate: func(s *adaptiveSignals) { s.queueDepth = 3 },
			want:   LevelBalanced,
		},
		{
			name: "丢帧立即降档", cur: LevelBalanced,
			mutate: func(s *adaptiveSignals) { s.droppedDelta = 1 },
			want:   LevelLow, wantSwitch: true,
		},
		{
			name: "编码35ms连续2次降档", cur: LevelBalanced,
			mutate: func(s *adaptiveSignals) { s.encodeMs = 35; s.encodeHighCount = 2 },
			want:   LevelLow, wantSwitch: true,
		},
		{
			name: "编码35ms仅1次不降档", cur: LevelBalanced,
			mutate: func(s *adaptiveSignals) { s.encodeMs = 35; s.encodeHighCount = 1 },
			want:   LevelBalanced,
		},
		{
			name: "编码34ms连续2次不降档", cur: LevelBalanced,
			mutate: func(s *adaptiveSignals) { s.encodeMs = 34; s.encodeHighCount = 2 },
			want:   LevelBalanced,
		},
		{
			name: "质量档高负载降一档", cur: LevelQuality,
			mutate: func(s *adaptiveSignals) { s.queueDepth = 4 },
			want:   LevelBalanced, wantSwitch: true,
		},
		{
			name: "低档不再降档（下限保护）", cur: LevelLow,
			mutate: func(s *adaptiveSignals) { s.queueDepth = 8; s.droppedDelta = 9 },
			want:   LevelLow,
		},
		{
			name: "冷却期内不降档", cur: LevelBalanced,
			mutate: func(s *adaptiveSignals) {
				s.sinceLastSwitch = adaptiveSwitchCooldown - time.Millisecond
				s.droppedDelta = 1
			},
			want: LevelBalanced,
		},
		{
			name: "冷却期满可降档", cur: LevelBalanced,
			mutate: func(s *adaptiveSignals) {
				s.sinceLastSwitch = adaptiveSwitchCooldown
				s.droppedDelta = 1
			},
			want: LevelLow, wantSwitch: true,
		},
		{
			name: "冷却期内不升档", cur: LevelBalanced,
			mutate: func(s *adaptiveSignals) {
				s.sinceLastSwitch = adaptiveSwitchCooldown - time.Millisecond
				s.encodeMs = 20
				s.stableSince = time.Hour
			},
			want: LevelBalanced,
		},
		{
			name: "低负载28ms持续15s升档", cur: LevelBalanced,
			mutate: func(s *adaptiveSignals) { s.encodeMs = 28; s.stableSince = adaptiveUpgradeStable },
			want:   LevelQuality, wantSwitch: true,
		},
		{
			name: "低负载29ms不升档", cur: LevelBalanced,
			mutate: func(s *adaptiveSignals) { s.encodeMs = 29; s.stableSince = adaptiveUpgradeStable },
			want:   LevelBalanced,
		},
		{
			name: "低负载未满15s不升档", cur: LevelBalanced,
			mutate: func(s *adaptiveSignals) {
				s.encodeMs = 28
				s.stableSince = adaptiveUpgradeStable - time.Millisecond
			},
			want: LevelBalanced,
		},
		{
			name: "队列非空不升档", cur: LevelLow,
			mutate: func(s *adaptiveSignals) {
				s.encodeMs = 20
				s.queueDepth = 1
				s.stableSince = adaptiveUpgradeStable
			},
			want: LevelLow,
		},
		{
			name: "有丢帧不升档", cur: LevelLow,
			mutate: func(s *adaptiveSignals) {
				s.encodeMs = 20
				s.droppedDelta = 1
				s.stableSince = adaptiveUpgradeStable
			},
			want: LevelLow,
		},
		{
			name: "最高档不再升档（上限保护）", cur: LevelQuality,
			mutate: func(s *adaptiveSignals) { s.encodeMs = 20; s.stableSince = adaptiveUpgradeStable },
			want:   LevelQuality,
		},
		{
			name: "降档优先于升档", cur: LevelBalanced,
			mutate: func(s *adaptiveSignals) {
				s.encodeMs = 35
				s.encodeHighCount = 2
				s.stableSince = time.Hour
			},
			want: LevelLow, wantSwitch: true,
		},
		{
			name: "非法负档位收敛到低档", cur: Level(-3),
			mutate: func(s *adaptiveSignals) { s.queueDepth = 4 },
			want:   LevelLow,
		},
		{
			name: "非法高档位收敛到质量档并降档", cur: Level(9),
			mutate: func(s *adaptiveSignals) { s.queueDepth = 4 },
			want:   LevelBalanced, wantSwitch: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sig := okSignals()
			if tc.mutate != nil {
				tc.mutate(&sig)
			}
			got, switched := decideAdaptive(tc.cur, sig)
			if got != tc.want || switched != tc.wantSwitch {
				t.Fatalf("decideAdaptive(%d) = (%d, %v)，期望 (%d, %v)",
					tc.cur, got, switched, tc.want, tc.wantSwitch)
			}
		})
	}
}

// TestLevelParams 校验档位参数表与非法档位的回退值（bitrate 单位 bit/s）。
func TestLevelParams(t *testing.T) {
	cases := []struct {
		level      Level
		bitrate    int
		complexity int
	}{
		{LevelLow, 96000, 3},
		{LevelBalanced, 128000, 5},
		{LevelQuality, 160000, 7},
		{Level(99), 128000, 5}, // 非法档位按平衡档处理
	}
	for _, c := range cases {
		bitrate, complexity := c.level.params()
		if bitrate != c.bitrate || complexity != c.complexity {
			t.Errorf("Level(%d).params() = (%d, %d)，期望 (%d, %d)",
				c.level, bitrate, complexity, c.bitrate, c.complexity)
		}
	}
}

// TestAdaptiveStateSample 覆盖控制器状态机：单次超阈值不降档、连续 2 次降档、
// 切换冷却、提交后重新累计低负载时长再升档。
func TestAdaptiveStateSample(t *testing.T) {
	c := newAdaptiveState()
	base := time.Now()

	// 第 1 次采样：编码延迟超阈值，但只有 1 次，不降档
	if _, change := c.sample(base, 40, 0, 0); change {
		t.Fatal("单次编码延迟超阈值不应降档")
	}
	// 第 2 次连续超阈值 → 降档到低档
	level, change := c.sample(base.Add(adaptiveSampleInterval), 40, 0, 0)
	if !change || level != LevelLow {
		t.Fatalf("连续 2 次超阈值应降档到 LevelLow，实际 (%d, %v)", level, change)
	}
	c.commit(level, base.Add(adaptiveSampleInterval))

	// 冷却期内（距切换 4s）即使严重积压也不切换
	if _, change := c.sample(base.Add(6*time.Second), 20, 8, 0); change {
		t.Fatal("冷却期内不应切换档位")
	}
	// 冷却期满（距切换 8s，变化点 base+10s）且低负载 → 开始累计
	if _, change := c.sample(base.Add(10*time.Second), 20, 0, 0); change {
		t.Fatal("低负载刚开始累计，不应立即升档")
	}
	// 低负载持续 14s（base+24s）仍不足 15s
	if _, change := c.sample(base.Add(24*time.Second), 20, 0, 0); change {
		t.Fatal("低负载未满 15s 不应升档")
	}
	// 低负载持续 16s → 升回平衡档
	level, change = c.sample(base.Add(26*time.Second), 20, 0, 0)
	if !change || level != LevelBalanced {
		t.Fatalf("低负载持续 16s 应升档到 LevelBalanced，实际 (%d, %v)", level, change)
	}
}

// TestAdaptiveStateDroppedBaseline 覆盖丢帧增量基线：运行起点之前的累计丢帧
// 不能被算作本次丢帧；本次新丢帧立即触发降档。
func TestAdaptiveStateDroppedBaseline(t *testing.T) {
	base := time.Now()

	// 未 seed：首个采样只建立基线，历史累计丢帧不触发降档
	c := newAdaptiveState()
	if _, change := c.sample(base, 24, 0, 7); change {
		t.Fatal("首个采样只建立丢帧基线，不应降档")
	}

	// seed：运行起点累计丢帧 5，首个采样 delta=0 不降档；新丢 1 帧立即降档
	c = newAdaptiveState()
	c.seedDropped(5)
	if _, change := c.sample(base, 24, 0, 5); change {
		t.Fatal("seed 的基线丢帧不应算作本次丢帧")
	}
	level, change := c.sample(base.Add(adaptiveSampleInterval), 24, 0, 6)
	if !change || level != LevelLow {
		t.Fatalf("新丢帧应立即降档，实际 (%d, %v)", level, change)
	}
}

// TestEngineEncoderParamsDefaults 校验引擎暴露的编码参数默认值与复位逻辑
// （Start 复位、未启动时也有合理默认值）。
func TestEngineEncoderParamsDefaults(t *testing.T) {
	e := NewEngine(KindSystem, ModeCapture, -1, func([]byte) {})
	bitrate, complexity, level := e.EncoderParams()
	if bitrate != 128000 || complexity != 5 || level != int(LevelBalanced) {
		t.Fatalf("默认编码参数应为平衡档 128000/5/1，实际 %d/%d/%d", bitrate, complexity, level)
	}
	if e.QueueDepth() != 0 || e.DroppedFrames() != 0 {
		t.Fatalf("初始队列深度/丢帧应为 0，实际 %d/%d", e.QueueDepth(), e.DroppedFrames())
	}

	// 模拟上一次运行已自适应到低档，Start 复位后应回到平衡档
	e.encBitrate.Store(96000)
	e.encComplexity.Store(3)
	e.encLevel.Store(int64(LevelLow))
	e.encQueueDepth.Store(3)
	e.resetEncoderParams()
	bitrate, complexity, level = e.EncoderParams()
	if bitrate != 128000 || complexity != 5 || level != int(LevelBalanced) {
		t.Fatalf("复位后应为平衡档 128000/5/1，实际 %d/%d/%d", bitrate, complexity, level)
	}
	if e.QueueDepth() != 0 {
		t.Fatalf("复位后队列深度应为 0，实际 %d", e.QueueDepth())
	}
}

// TestEngineAdaptiveDowngrade 是编码循环内的集成验证：制造编码队列丢帧
// （保护信号），最迟 2s 采样点应把档位降到省 CPU 档，并通过 EncoderParams
// 暴露新参数；降档后编码链路仍持续产出 Opus 包。
func TestEngineAdaptiveDowngrade(t *testing.T) {
	var packets atomic.Int64
	e := NewEngine(KindSystem, ModeCapture, -1, func([]byte) { packets.Add(1) })
	e.SetSubscribers(1)
	e.encQueue = make(chan queuedFrame, 8)
	e.encDone = make(chan struct{})
	e.isRunning.Store(true)
	go e.encodeLoop(e.encQueue, e.encDone)
	defer func() {
		e.isRunning.Store(false)
		close(e.encDone)
	}()

	// 持续按采集节奏喂 10ms 块：自适应采样只随帧处理推进，空闲时不采样
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				e.emitPCM(make([]byte, 3840)) // 10ms 立体声 float32
			}
		}
	}()

	// 突发投递远超队列容量（8）的 20ms 帧，制造编码队列丢帧
	for i := 0; i < 500; i++ {
		e.emitPCM(make([]byte, 7680))
	}

	// 最迟 2s 采样点应据此立即降档到省 CPU 档（96000 / complexity 3）
	waitCond(t, 4*time.Second, func() bool {
		bitrate, complexity, level := e.EncoderParams()
		return bitrate == 96000 && complexity == 3 && level == int(LevelLow)
	})

	close(stop)
	wg.Wait()
	if got := packets.Load(); got == 0 {
		t.Fatal("自适应降档后编码链路应持续产出 Opus 包")
	}
}
