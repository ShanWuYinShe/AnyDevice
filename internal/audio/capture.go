package audio

import (
	"context"
	"fmt"
	"math"
	"runtime"
	"sync"
	"time"

	"github.com/gen2brain/malgo"
)

const (
	SampleRate = 48000
	Channels   = 2
	BitDepth   = 16 // S16: 2 bytes per sample
	FrameSize  = Channels * (BitDepth / 8) // 4 bytes per stereo frame
)

type CaptureMode string

const (
	ModeAuto     CaptureMode = "auto"
	ModeLoopback CaptureMode = "loopback"
	ModeCapture  CaptureMode = "capture"
	ModeTestTone CaptureMode = "test-tone"
)

type Engine struct {
	mode        CaptureMode
	deviceIndex int
	onChunk     func([]byte)

	ctx       *malgo.AllocatedContext
	device    *malgo.Device
	cancelGen context.CancelFunc

	mu        sync.Mutex
	isRunning bool
	devName   string
}

func NewEngine(mode CaptureMode, deviceIndex int, onChunk func([]byte)) *Engine {
	return &Engine{
		mode:        mode,
		deviceIndex: deviceIndex,
		onChunk:     onChunk,
	}
}

// DeviceName returns the active audio source name.
func (e *Engine) DeviceName() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.devName
}

// Start begins capturing or generating audio.
func (e *Engine) Start() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.isRunning {
		return nil
	}

	if e.mode == ModeTestTone {
		e.devName = "Synthetic Test Tone (Dual Harmonic 440Hz)"
		e.startTestTone()
		e.isRunning = true
		return nil
	}

	// Try hardware capture via malgo
	ctx, err := malgo.InitContext(nil, malgo.ContextConfig{}, nil)
	if err != nil {
		fmt.Printf("[Audio] Malgo context init failed: %v. Falling back to test tone.\n", err)
		e.devName = "Synthetic Test Tone (Audio Driver Fallback)"
		e.startTestTone()
		e.isRunning = true
		return nil
	}
	e.ctx = ctx

	// Determine actual mode
	targetMode := e.mode
	if targetMode == ModeAuto {
		if runtime.GOOS == "windows" {
			targetMode = ModeLoopback
		} else {
			targetMode = ModeCapture
		}
	}

	var devType malgo.DeviceType
	if targetMode == ModeLoopback {
		devType = malgo.Loopback
	} else {
		devType = malgo.Capture
	}

	deviceConfig := malgo.DefaultDeviceConfig(devType)
	deviceConfig.Capture.Format = malgo.FormatS16
	deviceConfig.Capture.Channels = Channels
	deviceConfig.SampleRate = SampleRate
	deviceConfig.PeriodSizeInMilliseconds = 20 // 20ms chunks

	// If a specific device index is provided, resolve device ID
	if e.deviceIndex >= 0 {
		var listType malgo.DeviceType
		if devType == malgo.Loopback {
			listType = malgo.Playback
		} else {
			listType = malgo.Capture
		}

		devices, err := e.ctx.Devices(listType)
		if err == nil && e.deviceIndex < len(devices) {
			targetDev := devices[e.deviceIndex]
			e.devName = targetDev.Name()
			if devType == malgo.Loopback {
				deviceConfig.Playback.DeviceID = targetDev.ID.Pointer()
			} else {
				deviceConfig.Capture.DeviceID = targetDev.ID.Pointer()
			}
		}
	} else {
		// Auto detect virtual device if available
		e.autoSelectVirtualDevice(&deviceConfig, devType)
	}

	callbacks := malgo.DeviceCallbacks{
		Data: func(pOutputSample, pInputSamples []byte, framecount uint32) {
			if len(pInputSamples) > 0 && e.onChunk != nil {
				// Copy slice to ensure thread safety
				buf := make([]byte, len(pInputSamples))
				copy(buf, pInputSamples)
				e.onChunk(buf)
			}
		},
	}

	device, err := malgo.InitDevice(e.ctx.Context, deviceConfig, callbacks)
	if err != nil {
		fmt.Printf("[Audio] Warning: Failed to init audio device (%v). Falling back to test-tone.\n", err)
		if e.ctx != nil {
			e.ctx.Uninit()
			e.ctx.Free()
			e.ctx = nil
		}
		e.devName = "Synthetic Test Tone (No Audio Hardware Detected)"
		e.startTestTone()
		e.isRunning = true
		return nil
	}

	e.device = device
	if e.devName == "" {
		if devType == malgo.Loopback {
			e.devName = "System Default Output (Loopback)"
		} else {
			e.devName = "System Default Input"
		}
	}

	if err := e.device.Start(); err != nil {
		e.device.Uninit()
		e.ctx.Uninit()
		e.ctx.Free()
		e.ctx = nil
		e.device = nil
		fmt.Printf("[Audio] Device start failed: %v. Falling back to test-tone.\n", err)
		e.devName = "Synthetic Test Tone (Device Start Error)"
		e.startTestTone()
		e.isRunning = true
		return nil
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
			fmt.Printf("[Audio] Auto-detected virtual audio device: %s\n", name)
			e.devName = name
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
		e.device.Stop()
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
				for i := 0; i < framesPerChunk; i++ {
					t := float64(sampleIndex) / float64(SampleRate)
					// Gentle harmonic tone: 440Hz + 880Hz, modulated with slow pulsing
					amp := 0.15 * (0.8 + 0.2*math.Sin(2*math.Pi*0.5*t))
					sample1 := math.Sin(2 * math.Pi * 440 * t)
					sample2 := 0.5 * math.Sin(2*math.Pi*880*t)
					val := int16((sample1 + sample2) * amp * 32767 / 1.5)

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
					chunkCopy := make([]byte, len(buf))
					copy(chunkCopy, buf)
					e.onChunk(chunkCopy)
				}
			}
		}
	}()
}
