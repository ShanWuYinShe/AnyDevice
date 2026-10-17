// Web Audio 播放器：接收服务端 PCM（16bit 交错立体声 48kHz）并调度播放。
// 两台机器的音频时钟必然存在微小偏差（晶振漂移 + 网络抖动），若放任
// 播放位置与到达速率偏离，会周期性触发硬重置，听感为咔哒/杂音。
// 策略：小偏差用 ±2.5% 播放速率平滑追赶；大偏差（积压/断流）才整体重置。
let audioCtx = null;
let gainNode = null;
let nextAudioTime = 0;

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
  const drift = nextAudioTime - now;
  let rate = 1.0;

  if (nextAudioTime < now - 0.25 || drift > 0.35) {
    // 断流后重新起步 / 积压过多：丢弃偏差整体对齐（伴随一次可感知的跳变）
    nextAudioTime = now + 0.05;
  } else if (drift < -0.025) {
    // 播放位置落后 25ms 以上：轻微加速追赶
    rate = 1.025;
  } else if (drift > 0.025) {
    // 播放位置领先：轻微减速
    rate = 0.975;
  }

  const source = audioCtx.createBufferSource();
  source.buffer = audioBuffer;
  source.playbackRate.value = rate;
  source.connect(gainNode);
  source.start(nextAudioTime);
  // 追赶/减速会影响实际消耗时长，按速率折算后续调度位置
  nextAudioTime += audioBuffer.duration / rate;
}
