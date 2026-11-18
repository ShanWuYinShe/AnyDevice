// worklet 自适应抖动缓冲的数值验证：从 src/lib/audio.js 提取真实
// WORKLET_SOURCE，注入假 AudioWorkletProcessor/port，用虚拟时钟驱动，
// 锁定 anydevice-audio-streaming skill 记录的自适应行为断言：
//   初始 60ms；理想网络 ~20s 降到 40ms；断流 220ms 后目标 +30ms 且欠载
//   计 1 次；连续断流封顶 200ms；±8ms 抖动下不再欠载；丢包目标自动上调
//   且全程在 [40,200]；软限幅只衰减永不放大。改 worklet 自适应逻辑后
//   必须重跑：bun run test:worklet（CI macos job 已挂）。
// 断言全部基于确定性 PRNG（mulberry32），虚拟时钟无真实等待。
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import path from "node:path";

const here = path.dirname(fileURLToPath(import.meta.url));
const src = readFileSync(path.join(here, "../src/lib/audio.js"), "utf8");
const m = src.match(/const WORKLET_SOURCE = `([\s\S]*?)`;/);
if (!m) { console.error("FAIL 未在 audio.js 中找到 WORKLET_SOURCE"); process.exit(1); }
const workletSource = m[1];

// —— 假 worklet 环境 ——
class FakePort {
  constructor() { this.sent = []; this.onmessage = null; }
  postMessage(msg) { this.sent.push(msg); }
}
class FakeProcessor { constructor() { this.port = new FakePort(); } }

function makeWorklet(sampleRate, capacityFrames) {
  const factory = new Function(
    "sampleRate", "AudioWorkletProcessor", "registerProcessor",
    workletSource + "\nreturn { PCMRingPlayer };",
  );
  const { PCMRingPlayer } = factory(sampleRate, FakeProcessor, () => {});
  const w = new PCMRingPlayer({ processorOptions: { capacityFrames } });
  return w;
}

// 确定性 PRNG
function mulberry32(seed) {
  let a = seed >>> 0;
  return function () {
    a |= 0; a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

const SR = 48000;
const QUANTUM = 128;
const PACKET = 960; // 20ms @48kHz，与真实发送端 20ms 帧长一致

// 虚拟时钟驱动器：每 tick 前进一个 render quantum（先投递到期的 PCM 包，
// 再调 process()）。
class Driver {
  constructor(worklet) {
    this.w = worklet;
    this.frames = 0; // 已消耗的虚拟时间（帧）
    this.out = [new Float32Array(QUANTUM), new Float32Array(QUANTUM)];
    this.minTarget = 1e9;
    this.maxTarget = -1;
    this.maxOutAmp = 0;
  }
  feed(left, right) {
    this.w.port.onmessage({ data: { type: "pcm", left, right } });
  }
  feedStereo(n, amp) {
    const l = new Float32Array(n), r = new Float32Array(n);
    for (let i = 0; i < n; i++) { l[i] = amp; r[i] = amp; }
    this.feed(l, r);
  }
  tick() {
    const r = this.w.process([], [this.out]);
    if (!r) throw new Error("process 返回 false");
    for (let i = 0; i < QUANTUM; i++) {
      const a = Math.max(Math.abs(this.out[0][i]), Math.abs(this.out[1][i]));
      if (a > this.maxOutAmp) this.maxOutAmp = a;
    }
    this.frames += QUANTUM;
    if (this.w.targetMs < this.minTarget) this.minTarget = this.w.targetMs;
    if (this.w.targetMs > this.maxTarget) this.maxTarget = this.w.targetMs;
    return r;
  }
  // 推进 ms 毫秒虚拟时间，按节奏投包（jitterMs/lossPct 可选，确定性 PRNG）
  run(ms, opts) {
    const o = opts || {};
    const packetMs = o.packetMs || 20;
    const jitterMs = o.jitterMs || 0;
    const lossPct = o.lossPct || 0;
    const amp = o.amp === undefined ? 0.5 : o.amp;
    const rng = o.rng || mulberry32(1);
    const end = this.frames + Math.round((ms / 1000) * SR);
    let nextFeedAt = this.nextFeedAt === undefined ? this.frames : this.nextFeedAt;
    while (this.frames < end) {
      while (nextFeedAt <= this.frames) {
        if (rng() >= lossPct) this.feedStereo(PACKET, amp);
        const jit = jitterMs > 0 ? Math.round(((rng() * 2 - 1) * jitterMs / 1000) * SR) : 0;
        nextFeedAt += Math.max(1, PACKET + jit);
      }
      this.nextFeedAt = nextFeedAt;
      this.tick();
    }
    this.nextFeedAt = nextFeedAt;
  }
  stopFeed() { this.nextFeedAt = Infinity; }
  resumeFeed() { this.nextFeedAt = this.frames; }
  statsCount() { return this.w.port.sent.filter((s) => s.type === "stats").length; }
}

let failures = 0;
function check(name, cond, detail) {
  if (cond) { console.log("PASS " + name); }
  else { console.error("FAIL " + name + (detail ? " —— " + detail : "")); failures++; }
}

// S1+S2+S3：初始 60ms；priming 期输出静音；理想网络降到 40ms 下限
{
  const w = makeWorklet(SR, 96000);
  check("S1 初始目标 60ms", w.targetMs === 60, "实得 " + w.targetMs);
  const d = new Driver(w);
  let silentPrimed = true;
  if (w.priming) {
    let silent = true;
    // 只对「仍处于 priming 的量子」断言静音（水位达到目标后 priming
    // 结束、真实音频开始流出，之后的量子不应再断言静音）
    for (let q = 0; q < 8 && w.priming; q++) {
      d.feedStereo(PACKET, 0.5);
      d.tick();
      // 只断言「tick 后仍在 priming」的量子（静音路径）。水位达到目标的
      // 那个量子会翻转 priming 并开始输出真实音频，不算违例
      if (w.priming) {
        for (let i = 0; i < QUANTUM; i++) {
          if (d.out[0][i] !== 0 || d.out[1][i] !== 0) silent = false;
        }
      }
    }
    silentPrimed = silent;
  }
  check("S2 priming 期输出静音", silentPrimed);
  d.run(30000, { amp: 0.5 }); // 理想网络持续 30s
  check("S3 目标降到 40ms 下限", w.targetMs === 40, "30s 后实得 " + w.targetMs);
  check("S3 理想网络不再欠载", w.underflows === 0, "underflows=" + w.underflows);
  check("S8 stats 周期上报存在", d.statsCount() > 50, "stats 数 " + d.statsCount());
  check("S8 全程目标在 [40,200]", d.minTarget >= 40 && d.maxTarget <= 200,
    "min=" + d.minTarget + " max=" + d.maxTarget);
}

// S4：从稳态断流 220ms → 目标 +30ms、欠载计 1 次
{
  const w = makeWorklet(SR, 96000);
  const d = new Driver(w);
  d.run(30000, { amp: 0.5 }); // 进入 40ms 稳态
  const before = w.underflows;
  d.stopFeed();
  d.run(220);
  check("S4 断流欠载计 1 次", w.underflows === before + 1, "实得 " + (w.underflows - before));
  check("S4 目标 +30ms", w.targetMs === 70, "实得 " + w.targetMs);
  check("S4 重新进入 priming", w.priming === true);
}

// S5：连续断流封顶 200ms
{
  const w = makeWorklet(SR, 96000);
  const d = new Driver(w);
  d.run(30000, { amp: 0.5 });
  for (let cycle = 0; cycle < 8 && w.targetMs < 200; cycle++) {
    d.stopFeed(); d.run(250);
    // resume 喂流必须长于当前目标水位（≤200ms），否则 prime 无法完成、
    // 停流不再产生欠载，目标就卡在 190 上不去
    d.resumeFeed(); d.run(500);
  }
  check("S5 连续断流封顶 200ms", w.targetMs === 200, "实得 " + w.targetMs);
  check("S5 全程未超上限", d.maxTarget <= 200, "max=" + d.maxTarget);
}

// S6：±8ms 抖动下不再欠载（70ms 水位吸收投包抖动）。
// 用确定性交替 ±8ms（12ms/28ms 间隔交替，净漂移为零，单次最大缺口
// 8ms）。注意：若用均匀随机抖动，连续迟到的包会真实欠载——控制器抬
// 目标自愈，那是预期内的另一种行为，不在本断言范围。
{
  const w = makeWorklet(SR, 96000);
  const d = new Driver(w);
  d.run(30000, { amp: 0.5 });
  d.stopFeed(); d.run(120); // 触发一次欠载，目标抬到 70ms
  d.resumeFeed(); d.run(500); // 重新 prime
  const before = w.underflows;
  // 交替 12ms/28ms 间隔（±8ms 抖动）投 30s
  const end = d.frames + Math.round((30000 / 1000) * SR);
  let nextAt = d.nextFeedAt;
  let k = 0;
  while (d.frames < end) {
    while (nextAt <= d.frames) {
      d.feedStereo(PACKET, 0.5);
      nextAt += Math.round(((k % 2 === 0 ? 12 : 28) / 1000) * SR);
      k++;
    }
    d.nextFeedAt = nextAt;
    d.tick();
  }
  check("S6 ±8ms 抖动不再欠载", w.underflows === before, "新增 " + (w.underflows - before));
}

// S7：5% 丢包 → 目标自动上调，全程在 [40,200]
{
  const w = makeWorklet(SR, 96000);
  const d = new Driver(w);
  d.run(30000, { amp: 0.5 });
  const start = w.targetMs;
  d.run(60000, { lossPct: 0.05, rng: mulberry32(11) });
  check("S7 丢包下目标上调", w.targetMs > start, start + " → " + w.targetMs);
  check("S7 全程目标在 [40,200]", d.minTarget >= 40 && d.maxTarget <= 200,
    "min=" + d.minTarget + " max=" + d.maxTarget);
}

// S8：软限幅只衰减永不放大（喂 ±1.2 超界样本，输出不得超阈值）
{
  const w = makeWorklet(SR, 96000);
  const d = new Driver(w);
  d.run(200, { amp: 0.5 });
  for (let k = 0; k < 5; k++) d.feedStereo(PACKET, 1.2);
  d.run(40);
  check("S8 限幅输出不超过 0.95", d.maxOutAmp <= 0.95 + 1e-6, "峰值 " + d.maxOutAmp.toFixed(4));
}

if (failures > 0) { console.error("=== WORKLET-VALIDATE-FAILED: " + failures + " 项 ==="); process.exit(1); }
console.log("=== WORKLET-VALIDATE-ALL-PASS ===");