package audio

import (
	"context"
	"fmt"
	"math"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gen2brain/malgo"

	"any_device/internal/util"
)

const (
	SampleRate = 48000
	Channels   = 2
	BitDepth   = 32                        // F32: 4 bytes per sample
	FrameSize  = Channels * (BitDepth / 8) // 4 bytes per stereo frame
)

func isAllZero(b []byte) bool {
	for _, v := range b {
		if v != 0 {
			return false
		}
	}
	return true
}

type AudioKind string

const (
	KindSystem AudioKind = "system"
	KindMic    AudioKind = "mic"
)

var (
	TagSystemAudio = []byte("AUDS")
	TagMicAudio    = []byte("AUDM")
)

type CaptureMode string

const (
	ModeAuto     CaptureMode = "auto"
	ModeLoopback CaptureMode = "loopback"
	ModeCapture  CaptureMode = "capture"
	ModeTestTone CaptureMode = "test-tone"
)

type Engine struct {
	kind        AudioKind
	mode        CaptureMode
	deviceIndex int
	onChunk     func([]byte)

	ctx       *malgo.AllocatedContext
	device    *malgo.Device
	cancelGen context.CancelFunc

	mu          sync.Mutex
	isRunning   atomic.Bool
	isTestTone  bool // 当前来源为占位测试音（无真实虚拟声卡时的回退）
	devName     string
	subscribers int64 // 音频订阅数（atomic）：无订阅者时引擎静默，避免空转生成与广播 PCM

	// 诊断统计（ANY_DEVICE_AUDIO_DEBUG=1 时输出）：捕获 chunk 数/字节量/
	// 全零 chunk 数（SILENT 补丁后静音段表现为全零）/累计帧数。
	// 全部 atomic：emit 在音频实时回调线程执行，绝不能阻塞（曾因
	// 音频回调线程抢 e.mu 与 Start/Stop 死锁，服务整体卡死）。
	dbgChunks     atomic.Uint64
	dbgZeroChunks atomic.Uint64
	dbgBytes      atomic.Int64
	dbgFrames     atomic.Uint64
}

func NewEngine(kind AudioKind, mode CaptureMode, deviceIndex int, onChunk func([]byte)) *Engine {
	if kind == "" {
		kind = KindSystem
	}
	return &Engine{
		kind:        kind,
		mode:        mode,
		deviceIndex: deviceIndex,
		onChunk:     onChunk,
	}
}

// SetSubscribers 由 hub 在音频订阅数变化时回调更新；<=0 表示当前无人订阅音频。
func (e *Engine) SetSubscribers(n int64) {
	atomic.StoreInt64(&e.subscribers, n)
}

// DeviceName returns the active audio source name.
func (e *Engine) DeviceName() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.devName
}

// setDevName 线程安全地写入当前音频源名称。
func (e *Engine) setDevName(name string) {
	e.mu.Lock()
	e.devName = name
	e.mu.Unlock()
}

// Kind returns whether this engine captures system output or mic input.
func (e *Engine) Kind() AudioKind {
	return e.kind
}

// Start begins capturing or generating audio.
//
// 重要：本函数在检查 isRunning 后立即释放 e.mu——后续的 miniaudio
// 初始化是阻塞调用，期间音频回调线程可能已启动并调用 emit（需要
// 竞争统计锁），持锁初始化曾导致回调线程与主线程互等死锁。
// isRunning 仅在成功后加锁写入。
func (e *Engine) Start() error {
	if !e.isRunning.CompareAndSwap(false, true) {
		return nil
	}

	if e.mode == ModeTestTone {
		e.setDevName("系统扬声器 (未安装虚拟声卡)")
		e.startTestTone()

		return nil
	}

	// Windows 系统声音捕获路径：默认使用 miniaudio loopback（用户实测
	// 能正确捕获游戏音频），自研 WASAPI 仅在显式设置
	// ANY_DEVICE_NATIVE_WASAPI=1 时启用（其捕获内容经抓包分析为周期性
	// 方波噪声，捕不到真实音频，原因待查）。
	if runtime.GOOS == "windows" && e.kind == KindSystem && os.Getenv("ANY_DEVICE_NATIVE_WASAPI") != "" {
		captureCtx, cancel := context.WithCancel(context.Background())
		tag := TagSystemAudio
		if err := startNativeSystemCapture(captureCtx, func(pcm []byte) { e.emit(tag, pcm) }); err == nil {
			e.cancelGen = cancel
			e.setDevName("系统声音输出 (WASAPI 原生 Loopback)")

			return nil
		} else {
			cancel()
			util.Logf("[音频] 自研 WASAPI Loopback 启动失败，回退 miniaudio：%v", err)
		}
	}

	// Try hardware capture via malgo
	ctx, err := malgo.InitContext(nil, malgo.ContextConfig{}, nil)
	if err != nil {
		util.Logf("[Audio] Malgo context init failed for %s: %v\n", e.kind, err)
		if e.kind == KindSystem {
			e.setDevName("系统扬声器 (音频驱动不可用)")
			e.startTestTone()

			return nil
		}
		return fmt.Errorf("malgo init failed: %w", err)
	}
	e.ctx = ctx

	var devType malgo.DeviceType
	if e.kind == KindMic {
		devType = malgo.Capture
		e.setDevName("麦克风输入")
	} else {
		// System audio loopback
		if runtime.GOOS == "windows" {
			devType = malgo.Loopback
			e.setDevName("系统扬声器输出 (WASAPI Loopback)")
		} else {
			// On macOS / Linux: check for virtual loopback (BlackHole, Soundflower...)
			devType = malgo.Capture
		}
	}

	deviceConfig := malgo.DefaultDeviceConfig(devType)
	deviceConfig.Capture.Format = malgo.FormatF32
	deviceConfig.Capture.Channels = Channels
	deviceConfig.SampleRate = SampleRate
	// 周期使用设备默认值（不手动指定毫秒周期）：
	// WASAPI Loopback 下手动设 20ms 周期与设备实际周期（通常 10ms）不匹配，
	// 会导致约一半回调拿到 SILENT 空数据（表现为 20ms 声音/20ms 静音交替，
	// 听感为"嘟嘟"断续与电流声）。让 miniaudio 按设备周期驱动回调，
	// 配合 miniaudio 的 SILENT 清零补丁可获得连续音频流。

	if e.kind == KindMic {
		devices, err := e.ctx.Devices(malgo.Capture)
		if err == nil {
			if e.deviceIndex >= 0 && e.deviceIndex < len(devices) {
				e.setDevName(devices[e.deviceIndex].Name())
				deviceConfig.Capture.DeviceID = devices[e.deviceIndex].ID.Pointer()
			} else {
				for _, d := range devices {
					if d.IsDefault > 0 {
						e.setDevName(d.Name())
						deviceConfig.Capture.DeviceID = d.ID.Pointer()
						break
					}
				}
				if e.devName == "麦克风输入" && len(devices) > 0 {
					e.setDevName(devices[0].Name())
					deviceConfig.Capture.DeviceID = devices[0].ID.Pointer()
				}
			}
		}
	} else {
		// System audio
		if runtime.GOOS == "windows" {
			devices, err := e.ctx.Devices(malgo.Playback)
			if err == nil {
				if e.deviceIndex >= 0 && e.deviceIndex < len(devices) {
					e.setDevName(fmt.Sprintf("系统输出 [%s]", devices[e.deviceIndex].Name()))
					deviceConfig.Playback.DeviceID = devices[e.deviceIndex].ID.Pointer()
				} else {
					for _, d := range devices {
						if d.IsDefault > 0 {
							e.setDevName(fmt.Sprintf("系统输出 [%s]", d.Name()))
							deviceConfig.Playback.DeviceID = d.ID.Pointer()
							break
						}
					}
				}
			}
		} else {
			// macOS / Linux: Try finding virtual loopback device
			e.autoSelectVirtualDevice(&deviceConfig, devType)
			if e.devName == "" {
				// No virtual audio driver installed on macOS -> run test tone for system audio
				// Do NOT capture user microphone!
				e.setDevName("系统扬声器 (未安装虚拟声卡，无法捕获原声)")
				e.startTestTone()

				return nil
			}
		}
	}

	tag := TagSystemAudio
	if e.kind == KindMic {
		tag = TagMicAudio
	}

	// 诊断：每 2 秒输出一次捕获统计（ANY_DEVICE_AUDIO_DEBUG=1 时）
	if util.DebugAudio() {
		kindName := "系统声"
		if e.kind == KindMic {
			kindName = "麦克风"
		}
		go func() {
			var lastChunks, lastZero, lastBytes, lastFrames uint64
			for {
				time.Sleep(2 * time.Second)
				e.mu.Lock()
				c := e.dbgChunks.Load()
				z := e.dbgZeroChunks.Load()
				b := uint64(e.dbgBytes.Load())
				f := e.dbgFrames.Load()
				if c == lastChunks && lastBytes != 0 {
					util.Logf("[音频诊断] %s 捕获已停止增长（chunks=%d）", kindName, c)
					continue
				}
				nChunks := c - lastChunks
				avgFrames := 0
				if nChunks > 0 {
					avgFrames = int((f - lastFrames) / nChunks)
				}
				zeroRatio := 0.0
				if nChunks > 0 {
					zeroRatio = float64(z-lastZero) / float64(nChunks) * 100
				}
				util.Logf("[音频诊断] %s 捕获: %d chunks/2s, %d KB/2s, 平均 %d 帧/chunk, 全零 chunk %.0f%%",
					kindName, nChunks, (b-lastBytes)/1024, avgFrames, zeroRatio)
				lastChunks, lastZero, lastBytes, lastFrames = c, z, b, f
			}
		}()
	}

	callbacks := malgo.DeviceCallbacks{
		Data: func(_ []byte, pInputSamples []byte, _ uint32) {
			// 订阅门控 + tag 前缀 + 诊断统计统一由 emit 处理（与自研 WASAPI 路径一致）
			e.emit(tag, pInputSamples)
		},
	}

	device, err := malgo.InitDevice(e.ctx.Context, deviceConfig, callbacks)
	if err != nil {
		util.Logf("[Audio] Warning: Failed to init audio device for %s (%v).\n", e.kind, err)
		if e.kind == KindSystem {
			e.setDevName("系统扬声器 (未安装虚拟声卡)")
			e.startTestTone()

			return nil
		}
		return err
	}
	e.device = device

	if err := e.device.Start(); err != nil {
		e.device.Uninit()
		e.device = nil
		if e.kind == KindSystem {
			e.setDevName("系统扬声器 (未安装虚拟声卡)")
			e.startTestTone()

			return nil
		}
		return err
	}

	return nil
}

func (e *Engine) autoSelectVirtualDevice(config *malgo.DeviceConfig, devType malgo.DeviceType) {
	var listType malgo.DeviceType
	if devType == malgo.Loopback {
		listType = malgo.Playback
	} else {
		listType = malgo.Capture
	}

	devices, err := e.ctx.Devices(listType)
	if err != nil {
		return
	}

	for _, d := range devices {
		name := d.Name()
		if isVirtualDeviceName(name) {
			util.Logf("[Audio] Auto-detected virtual audio loopback: %s\n", name)
			e.setDevName(fmt.Sprintf("系统输出 [%s]", name))
			if devType == malgo.Loopback {
				config.Playback.DeviceID = d.ID.Pointer()
			} else {
				config.Capture.DeviceID = d.ID.Pointer()
			}
			return
		}
	}
}

// Stop stops capture or test tone.
func (e *Engine) Stop() {
	e.mu.Lock()
	defer e.mu.Unlock()

	if !e.isRunning.Load() {
		return
	}

	if e.cancelGen != nil {
		e.cancelGen()
		e.cancelGen = nil
	}

	if e.device != nil {
		_ = e.device.Stop()
		e.device.Uninit()
		e.device = nil
	}

	if e.ctx != nil {
		e.ctx.Uninit()
		e.ctx.Free()
		e.ctx = nil
	}

	e.isRunning.Store(false)
}

// emit 把 PCM 交付给订阅者：订阅门控、tag 前缀与诊断统计统一在此处理。
// 供 miniaudio 回调与自研 WASAPI 捕获共用，保证两条路径行为一致。
func (e *Engine) emit(tag []byte, pcm []byte) {
	if len(pcm) == 0 || e.onChunk == nil {
		return
	}
	if atomic.LoadInt64(&e.subscribers) <= 0 {
		return
	}
	if util.DebugAudio() {
		e.dbgChunks.Add(1)
		if isAllZero(pcm) {
			e.dbgZeroChunks.Add(1)
		}
		e.dbgBytes.Add(int64(len(pcm)))
		e.dbgFrames.Add(uint64(len(pcm) / 8))
	}
	buf := make([]byte, 4+len(pcm))
	copy(buf[:4], tag)
	copy(buf[4:], pcm)
	e.onChunk(buf)
}

// IsTestTone 报告当前系统声来源是否为占位测试音。
// 测试音仅用于远程接收端确认链路，不应广播给本机 UI 播放。
func (e *Engine) IsTestTone() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.isTestTone
}

// startTestTone 启动占位测试音（仅调试用，默认关闭）。
// 没有虚拟声卡时真实系统声无法捕获，此时静默并提示即可——
// 生成可听见的占位提示音只会干扰用户（映射真实音频时完全不需要）。
func (e *Engine) startTestTone() {
	if !util.EnableTestTone() {
		util.Logf("[音频] 系统声捕获不可用（未安装虚拟声卡/无默认输出设备）。" +
			"如需链路调试占位音，设置 ANY_DEVICE_TEST_TONE=1")
		return
	}
	e.mu.Lock()
	e.isTestTone = true
	e.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	e.cancelGen = cancel

	go func() {
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()

		var sampleIndex int64
		framesPerChunk := SampleRate / 50 // 960 frames for 20ms
		buf := make([]byte, framesPerChunk*FrameSize)

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				// 无订阅者时静默：跳过本 chunk 的生成（不停 ticker，恢复订阅后立即生效）
				if atomic.LoadInt64(&e.subscribers) <= 0 {
					continue
				}
				for i := 0; i < framesPerChunk; i++ {
					// 占位测试音使用「每 1.5 秒一短哔（660Hz，260ms）」的模式：
					// 与真实声音/环境噪声截然不同，一听即知是占位信号，
					// 避免连续双频正弦被误认为电流声或杂音。
					t := float64(sampleIndex) / float64(SampleRate)
					phase := math.Mod(t, 1.5)
					var val float32
					if phase < 0.26 {
						amp := 0.22 * math.Min(1.0, phase/0.02) * math.Min(1.0, (0.26-phase)/0.02)
						val = float32(math.Sin(2*math.Pi*660*t) * amp)
					}

					offset := i * FrameSize
					// 立体声交错 float32（Little Endian）
					lf := math.Float32bits(val)
					buf[offset] = byte(lf)
					buf[offset+1] = byte(lf >> 8)
					buf[offset+2] = byte(lf >> 16)
					buf[offset+3] = byte(lf >> 24)
					buf[offset+4] = byte(lf)
					buf[offset+5] = byte(lf >> 8)
					buf[offset+6] = byte(lf >> 16)
					buf[offset+7] = byte(lf >> 24)

					sampleIndex++
				}

				if e.onChunk != nil {
					chunkCopy := make([]byte, 4+len(buf))
					copy(chunkCopy[:4], TagSystemAudio)
					copy(chunkCopy[4:], buf)
					e.onChunk(chunkCopy)
				}
			}
		}
	}()
}
