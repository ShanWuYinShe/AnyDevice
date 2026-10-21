// Web Audio 播放器（AudioWorklet 环形缓冲版）。
//
// 为什么改：此前每个 PCM chunk 创建一个 BufferSource（每秒 100 个），
// 网络突发/抖动时调度会重置并造成多个 source 重叠播放（音量叠加=爆音"炸"）
// 与跳变（断续）。AudioWorklet + 环形缓冲是流媒体/VoIP 的标准做法：
//   - 主线程把 PCM 连续写入 worklet 的环形缓冲区；
//   - worklet 音频线程按采样时钟连续读取输出，天然无缝；
//   - 保留约 100ms 抖动缓冲吸收网络抖动；
//   - 缓冲溢出时丢弃最旧数据（延迟不累积），欠载时输出静音（不爆音）。
//
// 系统声（sys）与麦克风（mic）各持有一个独立的 ring 实例，互不干扰。

// AudioWorklet 处理器源码（内联加载，无需额外文件）
const WORKLET_SOURCE = `
class PCMRingPlayer extends AudioWorkletProcessor {
  constructor(options) {
    super();
    const cap = (options && options.processorOptions && options.processorOptions.capacityFrames) || 96000;
    this.cap = cap;
    this.ringL = new Float32Array(cap);
    this.ringR = new Float32Array(cap);
    this.writeIdx = 0;
    this.readIdx = 0;
    this.filled = 0;
    this.underflows = 0;
    this.overflows = 0;
    this.reportTimer = 0;
    // 预缓冲：积攒到 100ms 才开始出声（吸收网络抖动，避免开头碎裂）
    this.prebufferFrames = 4800;
    this.priming = true;
    // 软限幅器（专业音频链标准配置）：任何峰值过载平滑压缩，
    // 防止突发音量/叠加峰值造成削波爆音（"炸"）
    this.limiterGain = 1.0;
    this.port.onmessage = (e) => {
      const msg = e.data;
      if (!msg || msg.type !== 'pcm') return;
      const left = msg.left, right = msg.right;
      const n = left.length;
      // 溢出处理：丢弃最旧数据，保持延迟恒定
      if (this.filled + n > this.cap) {
        const drop = this.filled + n - this.cap;
        this.readIdx = (this.readIdx + drop) % this.cap;
        this.filled -= drop;
        this.overflows++;
      }
      for (let i = 0; i < n; i++) {
        this.ringL[this.writeIdx] = left[i];
        this.ringR[this.writeIdx] = right[i];
        this.writeIdx = (this.writeIdx + 1) % this.cap;
      }
      this.filled += n;
    };
  }
  process(inputs, outputs) {
    const out = outputs[0];
    const ch0 = out[0], ch1 = out[1];
    const n = ch0.length;
    // 预缓冲策略：缓冲不足时静音等待；发生欠载后重新积攒，避免碎音
    if (this.priming) {
      if (this.filled >= this.prebufferFrames) {
        this.priming = false;
      } else {
        for (let i = 0; i < n; i++) { ch0[i] = 0; ch1[i] = 0; }
        this.reportTimer++;
        if (this.reportTimer >= 187) {
          this.reportTimer = 0;
          this.port.postMessage({ type: 'stats', filled: this.filled, cap: this.cap, underflows: this.underflows, overflows: this.overflows });
        }
        return true;
      }
    }
    for (let i = 0; i < n; i++) {
      let l = 0, r = 0;
      if (this.filled > 0) {
        l = this.ringL[this.readIdx];
        r = this.ringR[this.readIdx];
        this.readIdx = (this.readIdx + 1) % this.cap;
        this.filled--;
      }
      // 软限幅（只衰减、永不放大——放大是失真"炸"的来源）：
      // 峰值超过阈值时把增益压到阈值以下，随后缓慢恢复到 1。
      const peak = Math.max(Math.abs(l), Math.abs(r));
      if (peak > 0.95) {
        this.limiterGain = Math.min(this.limiterGain, 0.95 / peak);
      } else if (this.limiterGain < 1.0) {
        this.limiterGain = Math.min(1.0, this.limiterGain + 0.0002);
      }
      ch0[i] = l * this.limiterGain;
      ch1[i] = r * this.limiterGain;
    }
    if (this.filled === 0) {
      this.underflows++;
      this.priming = true; // 断流后重新预缓冲，恢复时无碎音
    }
    // 每约 0.5 秒上报一次水位（128 帧/process → 约 187 次）
    this.reportTimer++;
    if (this.reportTimer >= 187) {
      this.reportTimer = 0;
      this.port.postMessage({ type: 'stats', filled: this.filled, cap: this.cap, underflows: this.underflows, overflows: this.overflows });
    }
    return true;
  }
}
registerProcessor('pcm-ring-player', PCMRingPlayer);
`;

let audioCtx = null;
let workletReady = false;
const nodes = { sys: null, mic: null };
const pending = { sys: [], mic: [] }; // worklet 就绪前的缓存
const streamStats = {
  sys: { filled: 0, cap: 0, underflows: 0, overflows: 0 },
  mic: { filled: 0, cap: 0, underflows: 0, overflows: 0 },
};
let workletError = '';

// 诊断快照：播放链路状态（UI 卡片显示）
export function audioContextState() {
  if (!audioCtx) return '未创建（未点击过播放）';
  let text = audioCtx.state + ' @ ' + audioCtx.sampleRate + 'Hz';
  if (workletError) return text + ' | AudioWorklet 加载失败: ' + workletError;
  const s = streamStats.sys;
  if (s.cap > 0) {
    const bufferMs = Math.round((s.filled / 48000) * 1000);
    text += ' | 缓冲 ' + bufferMs + 'ms | 欠载 ' + s.underflows + ' 次';
  }
  return text;
}

// AudioWorklet 不可用时的错误信息（前端自检上报用）
export function audioWorkletError() {
  return workletError;
}

export function ensureAudioContext() {
  if (audioCtx) {
    if (audioCtx.state === 'suspended') {
      audioCtx.resume();
    }
    return;
  }
  const Ctx = window.AudioContext || window.webkitAudioContext;
  audioCtx = new Ctx({ sampleRate: 48000 });
  if (!audioCtx.audioWorklet) {
    workletError = '浏览器不支持 AudioWorklet';
    return;
  }
  const blob = new Blob([WORKLET_SOURCE], { type: 'application/javascript' });
  const url = URL.createObjectURL(blob);
  audioCtx.audioWorklet
    .addModule(url)
    .then(() => {
      URL.revokeObjectURL(url);
      for (const key of ['sys', 'mic']) {
        const node = new AudioWorkletNode(audioCtx, 'pcm-ring-player', {
          numberOfOutputs: 1,
          outputChannelCount: [2],
          processorOptions: { capacityFrames: 96000 },
        });
        node.port.onmessage = (e) => {
          const m = e.data;
          if (m && m.type === 'stats') {
            streamStats[key].filled = m.filled;
            streamStats[key].cap = m.cap;
            streamStats[key].underflows = m.underflows;
            streamStats[key].overflows = m.overflows;
          }
        };
        node.connect(audioCtx.destination);
        nodes[key] = node;
        for (const msg of pending[key]) node.port.postMessage(msg);
        pending[key] = [];
      }
      workletReady = true;
      if (audioCtx.state === 'suspended') {
        audioCtx.resume();
      }
    })
    .catch((e) => {
      workletError = String((e && e.message) || e);
    });
}

// pushPCMFloats 把立体声 float 帧写入对应 ring
function pushPCMFloats(key, left, right) {
  const msg = { type: 'pcm', left, right };
  const node = nodes[key];
  if (node && workletReady) {
    node.port.postMessage(msg);
  } else if (pending[key].length < 200) {
    pending[key].push(msg);
  }
}

// playPCM 播放一个 PCM chunk（float32 交错立体声 48kHz）。
// stream: 'sys' | 'mic'
export function playPCM(arrayBuffer, stream) {
  if (!audioCtx) return;
  const key = stream === 'mic' ? 'mic' : 'sys';
  const f32 = new Float32Array(arrayBuffer);
  const frames = f32.length / 2;
  if (frames <= 0) return;
  const left = new Float32Array(frames);
  const right = new Float32Array(frames);
  for (let i = 0; i < frames; i++) {
    left[i] = f32[i * 2];
    right[i] = f32[i * 2 + 1];
  }
  pushPCMFloats(key, left, right);
}

// skipPCM 为静音段写入静音帧：保持环形缓冲的时间轴连续。
// （不写入会让后续音频紧接，导致时间轴缩短、播放速度异常）
export function skipPCM(arrayBuffer, stream) {
  if (!audioCtx) return;
  const key = stream === 'mic' ? 'mic' : 'sys';
  const frames = new Float32Array(arrayBuffer).length / 2;
  if (frames <= 0) return;
  pushPCMFloats(key, new Float32Array(frames), new Float32Array(frames));
}

// 调试快照（排查播放链路用，控制台/Debug 工具可查）
window.__audioDebug = () => ({
  hasCtx: !!audioCtx,
  ctxState: audioCtx ? audioCtx.state : null,
  workletReady,
  workletError,
  nodes: { sys: !!nodes.sys, mic: !!nodes.mic },
  pending: { sys: pending.sys.length, mic: pending.mic.length },
  stats: JSON.parse(JSON.stringify(streamStats)),
});


// —— Opus 解码（WebCodecs 原生）——
// 服务端推送 Opus 包（AUPS/AUPM tag 的 payload），此处解码为 float32
// 平面帧（左/右声道各一段）后送入对应环形缓冲。
//
// 两个 WebCodecs 硬约束（此前踩过）：
//   1) configure() 之后的第一块必须标记 type:'key'，否则 Chrome 抛
//      DataError: Failed to execute 'decode' on 'AudioDecoder'，整条链路无声；
//   2) AudioDecoder 输出的是平面（planar）数据，而 pushPCMFloats 接收
//      左右两段 Float32Array——不能把交错数据当单参数传进去（会静音/抛错）。
const opusDecoders = { sys: null, mic: null };
// 每个解码器实例的首块必须是 key frame；解码报错后需重置按关键帧重来
const opusNeedKey = { sys: true, mic: true };
let opusTsUs = 0;

function ensureOpusDecoder(key) {
  if (opusDecoders[key]) return;
  if (typeof AudioDecoder === 'undefined') {
    // 没有 AudioDecoder 的客户端（macOS Wails 窗口的 WKWebView、Safari）不会走到
    // 这里：连接建立时 set_mapping 已声明 pcm=true，服务端把对端音频解码成
    // float32 PCM 下发（AUDS/AUDM），由 playPCM 路径播放。这里只做兜底提示。
    workletError = '当前环境无 WebCodecs AudioDecoder（服务端将改为下发 PCM）';
    return;
  }
  const decoder = new AudioDecoder({
    output: (audioData) => {
      const frames = audioData.numberOfFrames;
      const channels = audioData.numberOfChannels;
      const l = new Float32Array(frames);
      const r = new Float32Array(frames);
      try {
        audioData.copyTo(l, { planeIndex: 0, format: 'f32-planar' });
        if (channels > 1) {
          audioData.copyTo(r, { planeIndex: 1, format: 'f32-planar' });
        } else {
          r.set(l); // 单声道包：复制到右声道
        }
      } finally {
        audioData.close();
      }
      pushPCMFloats(key, l, r);
    },
    error: (e) => {
      workletError = 'Opus 解码: ' + String((e && e.message) || e);
      opusNeedKey[key] = true;
    },
  });
  decoder.configure({ codec: 'opus', sampleRate: 48000, numberOfChannels: 2 });
  opusDecoders[key] = decoder;
  opusNeedKey[key] = true;
}

// decodeOpus 解码一个 Opus 包并送入对应流缓冲。
export function decodeOpus(packet, stream) {
  const key = stream === 'mic' ? 'mic' : 'sys';
  ensureOpusDecoder(key);
  const d = opusDecoders[key];
  if (!d) return;
  const type = opusNeedKey[key] ? 'key' : 'delta';
  opusTsUs += 20000; // 每包 20ms，时间戳仅用于顺序标记
  try {
    d.decode(new EncodedAudioChunk({
      type,
      timestamp: opusTsUs,
      data: new Uint8Array(packet),
    }));
    opusNeedKey[key] = false;
  } catch (e) {
    // 不让异常冒泡到 WebSocket 消息处理链（会打断后续包的接收）；
    // 记录到诊断状态并在下一包按关键帧重试
    workletError = 'Opus 解码: ' + String((e && e.message) || e);
    opusNeedKey[key] = true;
  }
}
