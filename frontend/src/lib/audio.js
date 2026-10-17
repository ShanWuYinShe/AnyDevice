// Web Audio 播放器：接收服务端 PCM（16bit 交错立体声 48kHz）并调度播放
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
  if (nextAudioTime < now || nextAudioTime > now + 0.3) {
    nextAudioTime = now + 0.05;
  }

  const source = audioCtx.createBufferSource();
  source.buffer = audioBuffer;
  source.connect(gainNode);
  source.start(nextAudioTime);
  nextAudioTime += audioBuffer.duration;
}
