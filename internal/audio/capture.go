package audio

import (
	"context"
	"fmt"
	"math"
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
	BitDepth   = 16                        // S16: 2 bytes per sample
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
	isRunning   bool
	devName     string
	subscribers int64 // 音频订阅数（atomic）：无订阅者时引擎静默，避免空转生成与广播 PCM

	// 诊断统计（ANY_DEVICE_AUDIO_DEBUG=1 时输出）：捕获 chunk 数/字节量/静音标记
	dbgChunks uint64
	dbgBytes  int
	dbgSilent bool
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

// Kind returns whether this engine captures system output or mic input.
func (e *Engine) Kind() AudioKind {
	return e.kind
}

// Start begins capturing or generating audio.
func (e *Engine) Start() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.isRunning {
		return nil
	}

	if e.mode == ModeTestTone {
		e.devName = "系统扬声器 (测试音输出)"
		e.startTestTone()
		e.isRunning = true
		return nil
	}

	// Try hardware capture via malgo
	ctx, err := malgo.InitContext(nil, malgo.ContextConfig{}, nil)
	if err != nil {
		fmt.Printf("[Audio] Malgo context init failed for %s: %v\n", e.kind, err)
		if e.kind == KindSystem {
			e.devName = "系统扬声器 (驱动回退测试音)"
			e.startTestTone()
			e.isRunning = true
			return nil
		}
		return fmt.Errorf("malgo init failed: %w", err)
	}
	e.ctx = ctx

	var devType malgo.DeviceType
	if e.kind == KindMic {
		devType = malgo.Capture
		e.devName = "麦克风输入"
	} else {
		// System audio loopback
		if runtime.GOOS == "windows" {
			devType = malgo.Loopback
			e.devName = "系统扬声器输出 (WASAPI Loopback)"
		} else {
			// On macOS / Linux: check for virtual loopback (BlackHole, Soundflower...)
			devType = malgo.Capture
		}
	}

	deviceConfig := malgo.DefaultDeviceConfig(devType)
	deviceConfig.Capture.Format = malgo.FormatS16
	deviceConfig.Capture.Channels = Channels
	deviceConfig.SampleRate = SampleRate
	deviceConfig.PeriodSizeInMilliseconds = 20 // 20ms chunks

	if e.kind == KindMic {
		devices, err := e.ctx.Devices(malgo.Capture)
		if err == nil {
			if e.deviceIndex >= 0 && e.deviceIndex < len(devices) {
				e.devName = devices[e.deviceIndex].Name()
				deviceConfig.Capture.DeviceID = devices[e.deviceIndex].ID.Pointer()
			} else {
				for _, d := range devices {
					if d.IsDefault > 0 {
						e.devName = d.Name()
						deviceConfig.Capture.DeviceID = d.ID.Pointer()
						break
					}
				}
				if e.devName == "麦克风输入" && len(devices) > 0 {
					e.devName = devices[0].Name()
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
					e.devName = fmt.Sprintf("系统输出 [%s]", devices[e.deviceIndex].Name())
					deviceConfig.Playback.DeviceID = devices[e.deviceIndex].ID.Pointer()
				} else {
					for _, d := range devices {
						if d.IsDefault > 0 {
							e.devName = fmt.Sprintf("系统输出 [%s]", d.Name())
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
				e.devName = "系统扬声器 (测试音 / 安装BlackHole可捕获原声)"
				e.startTestTone()
				e.isRunning = true
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
			var lastChunks, lastBytes uint64
			for {
				time.Sleep(2 * time.Second)
				e.mu.Lock()
				c, b, silent := e.dbgChunks, uint64(e.dbgBytes), e.dbgSilent
				e.mu.Unlock()
				if c == lastChunks && lastBytes != 0 {
					fmt.Printf("[音频诊断] %s 捕获已停止增长（chunks=%d）\n", kindName, c)
					continue
				}
				fmt.Printf("[音频诊断] %s 捕获: %d chunks/2s, %d KB/2s, 本段全零静音=%v\n", kindName, c-lastChunks, (b-lastBytes)/1024, silent)
				lastChunks, lastBytes = c, b
			}
		}()
	}

	callbacks := malgo.DeviceCallbacks{
		Data: func(pOutputSample, pInputSamples []byte, framecount uint32) {
			// 无订阅者时静默：直接返回，不分配内存也不调用 onChunk，避免空转
			if atomic.LoadInt64(&e.subscribers) <= 0 {
				return
			}
			if len(pInputSamples) > 0 && e.onChunk != nil {
				buf := make([]byte, 4+len(pInputSamples))
				copy(buf[:4], tag)
				copy(buf[4:], pInputSamples)
				// 诊断：统计实际捕获速率与字节量（含全零静音检测线索）
				if util.DebugAudio() {
					e.mu.Lock()
					e.dbgChunks++
					e.dbgBytes += len(pInputSamples)
					e.dbgSilent = isAllZero(pInputSamples)
					e.mu.Unlock()
				}
				e.onChunk(buf)
			}
		},
	}

	device, err := malgo.InitDevice(e.ctx.Context, deviceConfig, callbacks)
	if err != nil {
		fmt.Printf("[Audio] Warning: Failed to init audio device for %s (%v).\n", e.kind, err)
		if e.kind == KindSystem {
			e.devName = "系统扬声器 (测试音)"
			e.startTestTone()
			e.isRunning = true
			return nil
		}
		return err
	}
	e.device = device

	if err := e.device.Start(); err != nil {
		e.device.Uninit()
		e.device = nil
		if e.kind == KindSystem {
			e.devName = "系统扬声器 (测试音)"
			e.startTestTone()
			e.isRunning = true
			return nil
		}
		return err
	}

	e.isRunning = true
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
			fmt.Printf("[Audio] Auto-detected virtual audio loopback: %s\n", name)
			e.devName = fmt.Sprintf("系统输出 [%s]", name)
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

	if !e.isRunning {
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

	e.isRunning = false
}

// startTestTone generates smooth 440Hz + 880Hz audio chunks (20ms each, 960 frames) at low volume
func (e *Engine) startTestTone() {
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
					var val int16
					if phase < 0.26 {
						amp := 0.22 * math.Min(1.0, phase/0.02) * math.Min(1.0, (0.26-phase)/0.02)
						val = int16(math.Sin(2*math.Pi*660*t) * amp * 32767)
					}

					offset := i * FrameSize
					// Left channel (Little Endian)
					buf[offset] = byte(val)
					buf[offset+1] = byte(val >> 8)
					// Right channel
					buf[offset+2] = byte(val)
					buf[offset+3] = byte(val >> 8)

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
