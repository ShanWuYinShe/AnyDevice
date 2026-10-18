//go:build windows

package audio

// Windows WASAPI Loopback 捕获（纯 Go syscall 实现，不依赖 miniaudio）。
//
// 为什么自研：miniaudio 的 loopback 实现存在两个已实证的问题—
//  1. 不处理 AUDCLNT_BUFFERFLAGS_SILENT，静音段返回未定义数据（电流声）；
//  2. 固定回调周期与 WASAPI 设备周期不匹配时约一半回调拿到空数据，
//     形成 "20ms 声音 / 20ms 静音" 交替（嘟嘟声）。
// 本实现事件驱动读取音频包，精确处理 SILENT 标志与设备原生格式
// （float32/int16/int24/int32，任意采样率/声道），统一输出 48kHz 立体声 16bit。

import (
	"context"
	"errors"
	"fmt"
	"math"
	"syscall"
	"unsafe"

	"any_device/internal/util"
)

var (
	ole32    = syscall.NewLazyDLL("ole32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")

	procCoInitializeEx      = ole32.NewProc("CoInitializeEx")
	procCoUninitialize      = ole32.NewProc("CoUninitialize")
	procCoCreateInstance    = ole32.NewProc("CoCreateInstance")
	procCoTaskMemFree       = ole32.NewProc("CoTaskMemFree")
	procCreateEventW        = kernel32.NewProc("CreateEventW")
	procWaitForSingleObject = kernel32.NewProc("WaitForSingleObject")
	procCloseHandle         = kernel32.NewProc("CloseHandle")
)

const (
	coinitMultithreaded    = 0x0
	clsctxAll              = 0x17
	audclntShareModeShared = 0
	// AUDCLNT_STREAMFLAGS_*：loopback 捕获 + 事件回调
	audclntStreamFlagsLoopback      = 0x00020000
	audclntStreamFlagsEventCallback = 0x00040000
	// AUDCLNT_BUFFERFLAGS_SILENT：本段静音（数据未定义，必须按静音处理）
	audclntBufferFlagsSilent = 0x2
	waitTimeout              = 0x102
	waveFormatExtensible     = 0xFFFE
	waveFormatIEEEFloat      = 0x0003

	// 统一输出格式
	outSampleRate = 48000
)

type guid struct {
	Data1 uint32
	Data2 uint16
	Data3 uint16
	Data4 [8]byte
}

var (
	clsidMMDeviceEnumerator = guid{0xBCDE0395, 0xE52F, 0x467C, [8]byte{0x8E, 0x3D, 0xC4, 0x57, 0x92, 0x91, 0x69, 0x2E}}
	iidIMMDeviceEnumerator  = guid{0xA95664D2, 0x9614, 0x4F35, [8]byte{0xA7, 0x46, 0xDE, 0x8D, 0xB6, 0x36, 0x17, 0xE6}}
	iidIAudioClient         = guid{0x1CB9AD4C, 0xDBFA, 0x4C32, [8]byte{0xB1, 0x78, 0xC2, 0xF5, 0x68, 0xA7, 0x03, 0xB2}}
	iidIAudioCaptureClient  = guid{0xC8ADBD64, 0xE71E, 0x48A0, [8]byte{0xA4, 0xDE, 0x18, 0x5C, 0x39, 0x5C, 0xD3, 0x17}}
)

// callCom 调用 COM 接口 vtable 上第 index 个方法（0-based，含 IUnknown 前 3 个方法）。
func callCom(this unsafe.Pointer, index int, args ...uintptr) uintptr {
	vtbl := *(*unsafe.Pointer)(this)
	fn := *(*uintptr)(unsafe.Add(vtbl, index*int(unsafe.Sizeof(uintptr(0)))))
	all := make([]uintptr, 0, len(args)+1)
	all = append(all, uintptr(this))
	all = append(all, args...)
	ret, _, _ := syscall.SyscallN(fn, all...)
	return ret
}

func comRelease(p unsafe.Pointer) {
	if p != nil {
		callCom(p, 2)
	}
}

func hrFailed(hr uintptr) bool { return int32(hr) < 0 }

// waveFormatEx 与 WAVEFORMATEX 布局一致；cbSize 后可能跟随 WAVEFORMATEXTENSIBLE 扩展。
type waveFormatEx struct {
	FormatTag      uint16
	Channels       uint16
	SamplesPerSec  uint32
	AvgBytesPerSec uint32
	BlockAlign     uint16
	BitsPerSample  uint16
	CbSize         uint16
}

// wasapiCapture 封装一次 WASAPI Loopback 会话。
type wasapiCapture struct {
	enumerator    unsafe.Pointer
	device        unsafe.Pointer
	audioClient   unsafe.Pointer
	captureClient unsafe.Pointer
	event         uintptr

	// 设备原生格式
	srcIsFloat  bool
	srcChannels int
	srcRate     int
	srcBits     int

	// 流式重采样状态（原生采样率 → 48kHz）
	resampleRatio float64 // srcRate / 48000
	resampleFrac  float64 // 下一输出点在包内的浮点起始位置（可为负，跨包保留小数部分）

	// DC 高通滤波状态（一阶 IIR，20Hz 截止，去除设备直流偏移）。
	// 部分音频端点（如 HDMI）在无音频播放时 loopback 数据含巨大直流偏移，
	// 直接播放会造成低频"爆炸"声；高通去除 <20Hz 能量，对音频内容无损。
	hpX1L, hpY1L float64
	hpX1R, hpY1R float64
	lastL, lastR float64 // 上一包最后一帧（跨包插值用）
	haveLast     bool
}

// Close 释放全部 COM 资源与事件句柄。
func (w *wasapiCapture) Close() {
	if w.captureClient != nil {
		comRelease(w.captureClient)
		w.captureClient = nil
	}
	if w.audioClient != nil {
		callCom(w.audioClient, 11) // Stop
		comRelease(w.audioClient)
		w.audioClient = nil
	}
	if w.device != nil {
		comRelease(w.device)
		w.device = nil
	}
	if w.enumerator != nil {
		comRelease(w.enumerator)
		w.enumerator = nil
	}
	if w.event != 0 {
		procCloseHandle.Call(w.event)
		w.event = 0
	}
}

// startNativeSystemCapture 启动自研 WASAPI Loopback 捕获。
// 成功时在后台 goroutine 中持续回调 onPCM（48kHz S16 立体声交错）。
func startNativeSystemCapture(ctx context.Context, onPCM func([]byte)) error {
	for _, p := range []*syscall.LazyProc{procCoInitializeEx, procCoCreateInstance, procCreateEventW, procWaitForSingleObject, procCoTaskMemFree} {
		if err := p.Find(); err != nil {
			return fmt.Errorf("WASAPI 依赖不可用: %w", err)
		}
	}

	w := &wasapiCapture{}
	initialized := false
	hr, _, _ := procCoInitializeEx.Call(0, coinitMultithreaded)
	if !hrFailed(hr) || int32(hr) == 1 { // S_FALSE：当前线程已初始化
		initialized = true
	}
	fail := func(msg string, hr uintptr) error {
		w.Close()
		if initialized {
			procCoUninitialize.Call()
		}
		return fmt.Errorf("WASAPI: %s (hr=%#x)", msg, uint32(hr))
	}

	// IMMDeviceEnumerator
	var ppv unsafe.Pointer
	hr, _, _ = procCoCreateInstance.Call(
		uintptr(unsafe.Pointer(&clsidMMDeviceEnumerator)), 0, clsctxAll,
		uintptr(unsafe.Pointer(&iidIMMDeviceEnumerator)),
		uintptr(unsafe.Pointer(&ppv)))
	if hrFailed(hr) || ppv == nil {
		return fail("创建设备枚举器失败", hr)
	}
	w.enumerator = ppv

	// GetDefaultAudioEndpoint(eRender=0, eConsole=0)
	ppv = nil
	hr = callCom(w.enumerator, 4, 0, 0, uintptr(unsafe.Pointer(&ppv)))
	if hrFailed(hr) || ppv == nil {
		return fail("获取默认输出设备失败", hr)
	}
	w.device = ppv

	// Activate(IAudioClient)
	ppv = nil
	hr = callCom(w.device, 3,
		uintptr(unsafe.Pointer(&iidIAudioClient)), clsctxAll, 0,
		uintptr(unsafe.Pointer(&ppv)))
	if hrFailed(hr) || ppv == nil {
		return fail("激活 IAudioClient 失败", hr)
	}
	w.audioClient = ppv

	// GetMixFormat：设备原生混音格式（Initialize 需要显式传入，最后再释放）
	var pFormat *waveFormatEx
	hr = callCom(w.audioClient, 8, uintptr(unsafe.Pointer(&pFormat)))
	if hrFailed(hr) || pFormat == nil {
		return fail("获取混音格式失败", hr)
	}
	if err := w.parseFormat(pFormat); err != nil {
		procCoTaskMemFree.Call(uintptr(unsafe.Pointer(pFormat)))
		w.Close()
		if initialized {
			procCoUninitialize.Call()
		}
		return err
	}

	// 事件（自动重置，初始未触发）
	hEvent, _, _ := procCreateEventW.Call(0, 0, 0, 0)
	if hEvent == 0 {
		procCoTaskMemFree.Call(uintptr(unsafe.Pointer(pFormat)))
		w.Close()
		if initialized {
			procCoUninitialize.Call()
		}
		return errors.New("WASAPI: 创建事件句柄失败")
	}
	w.event = hEvent

	// Initialize：共享模式 + loopback + 事件回调（10ms 缓冲，显式传入设备混音格式）
	const hnsBufferDuration = 100000 // 10ms（100ns 单位）
	hr = callCom(w.audioClient, 3,
		audclntShareModeShared,
		uintptr(audclntStreamFlagsLoopback|audclntStreamFlagsEventCallback),
		hnsBufferDuration, 0,
		uintptr(unsafe.Pointer(pFormat)), 0)
	procCoTaskMemFree.Call(uintptr(unsafe.Pointer(pFormat)))
	if hrFailed(hr) {
		return fail("Initialize 失败", hr)
	}

	hr = callCom(w.audioClient, 13, hEvent) // SetEventHandle
	if hrFailed(hr) {
		return fail("SetEventHandle 失败", hr)
	}

	// GetService(IAudioCaptureClient)
	ppv = nil
	hr = callCom(w.audioClient, 14,
		uintptr(unsafe.Pointer(&iidIAudioCaptureClient)),
		uintptr(unsafe.Pointer(&ppv)))
	if hrFailed(hr) || ppv == nil {
		return fail("获取捕获客户端失败", hr)
	}
	w.captureClient = ppv

	hr = callCom(w.audioClient, 10) // Start
	if hrFailed(hr) {
		return fail("Start 失败", hr)
	}
	util.Logf("[音频] 自研 WASAPI Loopback 已启动（设备格式 %.1fkHz %dch %dbit%s，输出 48kHz 立体声）",
		float64(w.srcRate)/1000, w.srcChannels, w.srcBits, map[bool]string{true: " float", false: ""}[w.srcIsFloat])

	go func() {
		defer func() {
			w.Close()
			if initialized {
				procCoUninitialize.Call()
			}
		}()
		w.loop(ctx, onPCM)
	}()
	return nil
}

// parseFormat 解析 WAVEFORMATEX / WAVEFORMATEXTENSIBLE。
func (w *wasapiCapture) parseFormat(p *waveFormatEx) error {
	w.srcChannels = int(p.Channels)
	w.srcRate = int(p.SamplesPerSec)
	w.srcBits = int(p.BitsPerSample)
	if w.srcChannels < 1 || w.srcRate < 8000 || w.srcBits < 8 {
		return fmt.Errorf("WASAPI: 无法识别的混音格式 (channels=%d, rate=%d, bits=%d)", w.srcChannels, w.srcRate, w.srcBits)
	}
	w.srcIsFloat = p.FormatTag == waveFormatIEEEFloat
	if p.FormatTag == waveFormatExtensible {
		if p.CbSize < 22 {
			return fmt.Errorf("WASAPI: WAVEFORMATEXTENSIBLE 扩展长度异常 (cbSize=%d)", p.CbSize)
		}
		// WAVEFORMATEXTENSIBLE 扩展布局（紧接 WAVEFORMATEX 的 18 字节之后）：
		//   wValidBitsPerSample uint16（2 字节）
		//   dwChannelMask       uint32（4 字节）
		//   SubFormat           GUID （16 字节，偏移 +6）
		// KSDATAFORMAT_SUBTYPE_IEEE_FLOAT 的 Data1 = 3；PCM 的 Data1 = 1
		sub := (*guid)(unsafe.Add(unsafe.Pointer(p), unsafe.Sizeof(*p)+6))
		w.srcIsFloat = sub.Data1 == 3
	}
	w.resampleRatio = float64(w.srcRate) / float64(outSampleRate)
	w.resampleFrac = 0
	return nil
}

// loop 事件驱动读取音频包。
func (w *wasapiCapture) loop(ctx context.Context, onPCM func([]byte)) {
	for {
		if ctx.Err() != nil {
			return
		}
		r, _, _ := procWaitForSingleObject.Call(w.event, 100)
		if r == waitTimeout {
			continue
		}
		for {
			var packetFrames uint32
			hr := callCom(w.captureClient, 5, uintptr(unsafe.Pointer(&packetFrames)))
			if hrFailed(hr) || packetFrames == 0 {
				break
			}
			var pData unsafe.Pointer
			var nframes uint32
			var flags uint32
			hr = callCom(w.captureClient, 3,
				uintptr(unsafe.Pointer(&pData)),
				uintptr(unsafe.Pointer(&nframes)),
				uintptr(unsafe.Pointer(&flags)), 0, 0)
			if hrFailed(hr) || pData == nil || nframes == 0 {
				break
			}

			var pcm []byte
			if flags&audclntBufferFlagsSilent != 0 {
				// 静音段：缓冲区数据未定义，按静音输出（miniaudio 缺失的关键处理）
				pcm = w.renderSilence(int(nframes))
			} else {
				frames := unsafe.Slice((*byte)(pData), int(nframes)*w.frameBytes())
				pcm = w.convert(frames, int(nframes))
			}
			callCom(w.captureClient, 4, uintptr(nframes)) // ReleaseBuffer

			if len(pcm) > 0 && onPCM != nil {
				onPCM(pcm)
			}
			if ctx.Err() != nil {
				return
			}
		}
	}
}

// frameBytes 一帧的字节数。
func (w *wasapiCapture) frameBytes() int {
	return w.srcChannels * w.srcBits / 8
}

// readChannel 从一帧中读取第 ch 声道（返回 -32768..32767 范围的浮点值）。
func (w *wasapiCapture) readChannel(data []byte, frame int, ch int) float64 {
	base := frame*w.frameBytes() + ch*w.srcBits/8
	if base+w.srcBits/8 > len(data) {
		return 0
	}
	switch {
	case w.srcIsFloat && w.srcBits == 32:
		v := math.Float32frombits(*(*uint32)(unsafe.Pointer(&data[base])))
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
			return 0
		}
		return float64(v) * 32767.0
	case w.srcBits == 16:
		return float64(int16(*(*uint16)(unsafe.Pointer(&data[base]))))
	case w.srcBits == 24:
		v := int32(data[base]) | int32(data[base+1])<<8 | int32(data[base+2])<<16
		if v&0x800000 != 0 {
			v -= 1 << 24
		}
		return float64(v) / 256.0
	case w.srcBits == 32:
		v := int32(*(*uint32)(unsafe.Pointer(&data[base])))
		return float64(v) / 65536.0
	}
	return 0
}

// toS16 钳位并编码为 int16。
func toS16(v float64) int16 {
	if v > 32767 {
		return 32767
	}
	if v < -32768 {
		return -32768
	}
	return int16(v)
}

// convert 原生帧序列 → 48kHz 立体声 S16 交错（流式线性重采样，跨包保留小数位置）。
func (w *wasapiCapture) convert(data []byte, nframes int) []byte {
	// 目标输出帧数（含跨包余量）
	outCap := int(float64(nframes)/w.resampleRatio) + 2
	out := make([]byte, 0, outCap*4)

	// 快路径：48kHz 直通（无重采样损失）
	if w.srcRate == outSampleRate {
		for i := 0; i < nframes; i++ {
			l := w.readChannel(data, i, 0)
			r := l
			if w.srcChannels > 1 {
				r = w.readChannel(data, i, 1)
			}
			out = w.appendFrame(out, l, r)
		}
		if nframes > 0 {
			w.lastL = w.readChannel(data, nframes-1, 0)
			w.lastR = w.lastL
			if w.srcChannels > 1 {
				w.lastR = w.readChannel(data, nframes-1, 1)
			}
			w.haveLast = true
		}
		return out
	}

	// 重采样：pos 为本包内的浮点位置（可为负，表示需要上一包最后一帧插值）
	pos := w.resampleFrac
	for {
		if pos >= float64(nframes)-1 {
			break // 末尾一个采样点留到下一包（需要右邻样本做插值）
		}
		i := int(math.Floor(pos))
		a := pos - float64(i)

		var l0, r0, l1, r1 float64
		if i < 0 {
			if !w.haveLast {
				if pos < -1 {
					break
				}
				l0, r0 = 0, 0
			} else {
				l0, r0 = w.lastL, w.lastR
			}
			l1 = w.readChannel(data, 0, 0)
			r1 = l1
			if w.srcChannels > 1 {
				r1 = w.readChannel(data, 0, 1)
			}
		} else {
			l0 = w.readChannel(data, i, 0)
			r0 = l0
			if w.srcChannels > 1 {
				r0 = w.readChannel(data, i, 1)
			}
			l1 = w.readChannel(data, i+1, 0)
			r1 = l1
			if w.srcChannels > 1 {
				r1 = w.readChannel(data, i+1, 1)
			}
		}
		out = w.appendFrame(out,
			l0+(l1-l0)*a,
			r0+(r1-r0)*a)
		pos += w.resampleRatio
	}
	// 保存跨包状态
	if nframes > 0 {
		w.lastL = w.readChannel(data, nframes-1, 0)
		w.lastR = w.lastL
		if w.srcChannels > 1 {
			w.lastR = w.readChannel(data, nframes-1, 1)
		}
		w.haveLast = true
	}
	w.resampleFrac = pos - float64(nframes)
	return out
}

// renderSilence 生成与输入时长等价的静音输出，并同步推进重采样状态
// （静音段同样消耗时间轴，否则恢复音频后输出节奏会漂移）。
func (w *wasapiCapture) renderSilence(nframes int) []byte {
	pos := w.resampleFrac
	outFrames := 0
	for pos < float64(nframes) {
		pos += w.resampleRatio
		outFrames++
	}
	w.resampleFrac = pos - float64(nframes)
	return make([]byte, outFrames*4)
}

// dcHighPass 一阶 IIR 高通（20Hz 截止 @48kHz），去除设备直流偏移。
// 无音频播放时部分端点（HDMI 等）的 loopback 数据带巨大 DC 分量，
// 直出会造成低频爆炸声；高通对正常音频内容无影响。
func (w *wasapiCapture) dcHighPass(x float64, x1, y1 *float64) float64 {
	const alpha = 0.99739 // 1/(1+2π·20/48000)
	y := alpha * (*y1 + x - *x1)
	*x1 = x
	*y1 = y
	return y
}

// appendFrame 应用 DC 高通后追加一帧立体声 S16。
func (w *wasapiCapture) appendFrame(buf []byte, l, r float64) []byte {
	l = w.dcHighPass(l, &w.hpX1L, &w.hpY1L)
	r = w.dcHighPass(r, &w.hpX1R, &w.hpY1R)
	li, ri := toS16(l), toS16(r)
	return append(buf, byte(li), byte(li>>8), byte(ri), byte(ri>>8))
}
