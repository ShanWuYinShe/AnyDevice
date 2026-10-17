// Web Audio 播放器：接收服务端 PCM（16bit 交错立体声 48kHz）并调度播放。
// 调度策略（保守）：仅当播放位置与到达速率严重偏离（断流/积压）时重置对齐；
// 不做 playbackRate 微调——微调会与目标系统的音频时钟交互产生不确定性。
let audioCtx = null;
let gainNode = null;
let nextAudioTime = 0;

// 诊断快照：供 UI 显示播放链路状态（收到速率由 state 模块统计）
export function audioContextState() {
  if (!audioCtx) return '未创建（未点击过播放）';
  return audioCtx.state + ' @ ' + audioCtx.sampleRate + 'Hz';
}

export function ensureAudioContext() {
  if (!audioCtx) {
    const Ctx = window.AudioContext || window.webkitAudioContext;
    audioCtx = new Ctx({ sampleRate: 48000 });
    gainNode = audioCtx.createGain();
    gainNode.gain.value = 1.0;
    gainNode.connect(audioCtx.destination);
  }
  if (audioCtx.state === 'suspended') {
    audioCtx.resume();
  }
}

export function playPCM(arrayBuffer) {
  if (!audioCtx) return;
  const int16 = new Int16Array(arrayBuffer);
  const frameCount = int16.length / 2;
  if (frameCount <= 0) return;

  const audioBuffer = audioCtx.createBuffer(2, frameCount, 48000);
  const left = audioBuffer.getChannelData(0);
  const right = audioBuffer.getChannelData(1);
  for (let i = 0; i < frameCount; i++) {
    left[i] = int16[i * 2] / 32768.0;
    right[i] = int16[i * 2 + 1] / 32768.0;
  }

  const now = audioCtx.currentTime;
  // 保守调度：仅当严重落后（断流积压）或异常领先时重置对齐
  if (nextAudioTime < now - 0.25 || nextAudioTime > now + 0.35) {
    nextAudioTime = now + 0.05;
  }

  const source = audioCtx.createBufferSource();
  source.buffer = audioBuffer;
  source.connect(gainNode);
  source.start(nextAudioTime);
  nextAudioTime += audioBuffer.duration;
}
