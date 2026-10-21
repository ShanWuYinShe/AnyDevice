// 全局响应式状态（Svelte 5 runes 模块）：数据轮询、WebSocket 流、订阅同步
import { api } from './api.js';
import { ensureAudioContext, playPCM, skipPCM, decodeOpus, audioContextState } from './audio.js';

// —— 可变状态（对象属性形式，跨模块保持响应式） ——
export const selfState = $state({ data: null }); // /api/self：本机信息
export const peerState = $state({ list: [] });   // /api/peers：已发现对端（服务端已过滤查看者自己）
// modal: '' | 'qr' | 'add' | 'self' | 'rename'
// rename: null | { kind: 'self' | 'peer', id, currentName, value }
export const uiState = $state({ toast: '', toastType: 'info', modal: '', rename: null });
export const connState = $state({ wsOpen: false });
export const telemetry = $state({ cursor: {}, keyboard: {} }); // peerId -> 遥测文本

let toastTimer = null;
export function showToast(msg, type = 'info') {
  uiState.toast = msg;
  uiState.toastType = type;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => {
    uiState.toast = '';
    uiState.toastType = 'info';
  }, 2800);
}

// —— 派生状态 ——
// Svelte 5 不允许模块直接导出 $derived，这里导出取值函数（调用处保持响应式追踪）。
// 各对端映射的汇总播放标志（驱动本地音频播放与订阅同步）
export function playState() {
  return {
    system: peerState.list.some((p) => p.mapping && (p.mapping.play_system_audio_local || p.mapping.play_audio_local)),
    mic: peerState.list.some((p) => p.mapping && p.mapping.play_mic_audio_local),
  };
}
// 目标订阅组合：只有真正开启了映射才向服务端订阅对应流（本机客户端默认不订阅）
export function targetSubs() {
  const play = playState();
  return {
    audio: play.system || play.mic,
    mouse: peerState.list.some((p) => p.mapping && p.mapping.execute_mouse_local),
    keyboard: peerState.list.some((p) => p.mapping && p.mapping.execute_keyboard_local),
  };
}

let lastSubs = { audio: null, mouse: null, keyboard: null };

export function openModal(id) {
  uiState.modal = id;
}
export function closeModal() {
  uiState.modal = '';
  uiState.rename = null;
}

// isSelfPeer：服务端已按查看者过滤，这里是前端兜底
// （ID 匹配、回环地址、本机网卡 IP；不按名称判断，避免误伤同名设备）
export function isSelfPeer(p) {
  if (!p) return true;
  if (selfState.data && p.id && p.id === selfState.data.id) return true;

  const rawIP = (p.ip || '').trim();
  const cleanIP = rawIP.replace(/[\[\]]/g, '').split(':')[0].trim();
  if (!cleanIP || cleanIP === '127.0.0.1' || cleanIP === 'localhost' || cleanIP === '::1' || cleanIP === '0.0.0.0' || cleanIP.startsWith('127.')) {
    return true;
  }
  if (selfState.data) {
    const selfCleanIP = (selfState.data.current_ip || '').replace(/[\[\]]/g, '').split(':')[0].trim();
    if (selfCleanIP && cleanIP === selfCleanIP) return true;
    if (selfState.data.interfaces && selfState.data.interfaces.some((i) => i.ip && i.ip.trim() === cleanIP)) return true;
  }
  if (p.target) {
    const cleanTarget = p.target.replace(/[\[\]]/g, '').trim();
    const targetHost = cleanTarget.split(':')[0].trim();
    if (!targetHost || targetHost === '127.0.0.1' || targetHost === 'localhost' || targetHost === '::1' || targetHost.startsWith('127.')) {
      return true;
    }
    if (selfState.data) {
      const selfCleanIP = (selfState.data.current_ip || '').replace(/[\[\]]/g, '').split(':')[0].trim();
      if (selfCleanIP && targetHost === selfCleanIP) return true;
      if (selfState.data.interfaces && selfState.data.interfaces.some((i) => i.ip && i.ip.trim() === targetHost)) return true;
    }
  }
  return false;
}

// —— 数据轮询 ——
export async function fetchSelf() {
  try {
    selfState.data = await api.fetchSelf();
  } catch (e) {
    console.error('fetchSelf failed:', e);
  }
}

export async function fetchPeers() {
  try {
    if (!selfState.data) {
      await fetchSelf();
    }
    const data = await api.fetchPeers();
    // 服务端已按查看者过滤“自己”，这里再兜底过滤一次
    peerState.list = (data.peers || []).filter((p) => !isSelfPeer(p));
  } catch (e) {
    console.error('fetchPeers failed:', e);
  }
}

// 订阅同步：目标组合变化且 WS 就绪时发送 set_mapping
export function sendSubscriptions() {
  if (!connState.wsOpen) return;
  const t = targetSubs();
  if (lastSubs.audio === t.audio && lastSubs.mouse === t.mouse && lastSubs.keyboard === t.keyboard) return;
  // pcm：客户端没有 WebCodecs AudioDecoder（Wails macOS WKWebView / Safari）时
  // 声明 true，服务端把对端音频解码成 float32 PCM 再下发（见 server/hub.go）。
  ws.send(JSON.stringify({
    action: 'set_mapping',
    audio: t.audio,
    mouse: t.mouse,
    keyboard: t.keyboard,
    pcm: typeof AudioDecoder === 'undefined',
  }));
  lastSubs = { audio: t.audio, mouse: t.mouse, keyboard: t.keyboard };
}

// —— WebSocket 音频与遥测流 ——
let ws = null;
let reconnectTimer = null;

// isAllZeroPCM 判断 float32 交错 PCM 是否全零（静音段）。
// 全扫描开销在 50 帧/秒下可忽略，避免误伤真实音频开头的静音采样。
function isAllZeroPCM(pcm) {
  const f32 = new Float32Array(pcm);
  for (let i = 0; i < f32.length; i++) {
    if (f32[i] !== 0) return false;
  }
  return true;
}

// 音频接收实时统计（卡片上可见）：按流分类字节率（每 2 秒刷新一次）
// 必须同时覆盖两种协议，否则新协议下卡片恒显示 0 KB/s：
//   AUPS/AUPM = Opus（当前协议）· AUDS/AUDM = float32 PCM（历史协议）
const AUDIO_RATE_TAGS = { AUPS: 'sys', AUDS: 'sys', AUPM: 'mic', AUDM: 'mic' };
export const audioRate = $state({ sys: 0, mic: 0 });
let audioRateBytes = { sys: 0, mic: 0 };
let audioRateLast = 0;
// 播放链路状态描述（AudioContext 是否被系统挂起等）
export const audioPlayState = $state({ text: '未创建' });

// 是否为 Wails 原生桌面窗口：
//   darwin/linux 的页面 origin 是 wails://wails（自定义 scheme，由 Wails
//   的 WKWebView/WKURLSchemeHandler 承载，不是本项目真正的 HTTP 服务端）；
//   windows 是 http://wails.localhost（Wails 资产服务虚拟主机，同样不是监听地址）。
// 两者 location.host 都不能用于推导 /ws 地址，必须显式连本机回环端口。
function isDesktopShell() {
  if (location.protocol !== 'http:' && location.protocol !== 'https:') return true;
  return location.hostname === 'wails.localhost' || location.hostname.endsWith('.wails.localhost');
}

// 解析 WebSocket 基地址：
//   - 浏览器/手机：同源相对地址（location.host 就是服务监听地址）；
//   - 桌面窗口：ws://127.0.0.1:<真实端口>；端口以 /api/self 的 port 为准
//     （-port 可改端口）。相对路径 fetch('/api/...') 会经 Wails 的
//     AssetsHandler 直达本服务的 handler，因此 /api/self 在窗口内可用。
async function resolveWsBase() {
  if (!isDesktopShell()) {
    const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
    return proto + '//' + location.host;
  }
  if (!selfState.data) {
    try {
      await fetchSelf();
    } catch {
      // 拿不到本机信息时先按默认端口连接，后续轮询仍会刷新 selfState
    }
  }
  const port = (selfState.data && selfState.data.port) || 8080;
  return 'ws://127.0.0.1:' + port;
}

export async function connectWebSocket() {
  // 异步解析基地址期间可能被再次调用（轮询/重连），已有连接时直接跳过
  if (ws && (ws.readyState === WebSocket.CONNECTING || ws.readyState === WebSocket.OPEN)) return;
  const wsBase = await resolveWsBase();
  ws = new WebSocket(wsBase + '/ws');
  ws.binaryType = 'arraybuffer';

  // 前端自检上报：桌面窗口无控制台/DevTools，把连接状态 POST 给服务端日志
  const report = (event, detail) => {
    try {
      fetch('/api/client-report', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ event, detail: String(detail || ''), url: wsBase + '/ws' }),
      }).catch(() => {});
    } catch (e) { /* 忽略 */ }
  };

  ws.onopen = () => {
    connState.wsOpen = true;
    report('ws_open', 'readyState=' + ws.readyState);
    // 连接建立即同步当前订阅（服务端对本机客户端默认不订阅）
    sendSubscriptions();
  };

  ws.onerror = (e) => {
    report('ws_error', 'readyState=' + ws.readyState + ' type=' + (e && e.type));
  };

  ws.onmessage = (event) => {
    if (event.data instanceof ArrayBuffer) {
      // 接收速率统计：每 2 秒刷新到 audioRate（卡片可见）
      const nowMs = performance.now();
      if (event.data.byteLength > 4) {
        const t = String.fromCharCode(...new Uint8Array(event.data, 0, 4));
        const rateStream = AUDIO_RATE_TAGS[t];
        if (rateStream) audioRateBytes[rateStream] += event.data.byteLength;
      }
      if (nowMs - audioRateLast >= 2000) {
        const kb = (n) => Math.round((audioRateBytes[n] || 0) / 2 / 1024);
        audioRate.sys = kb('sys');
        audioRate.mic = kb('mic');
        audioRateBytes = { sys: 0, mic: 0 };
        audioRateLast = nowMs;
        // 同步刷新播放器状态（缓冲水位/欠载次数实时可见）
        audioPlayState.text = 'ctx=' + audioContextState();
      }
      if (event.data.byteLength > 4) {
        const tag = String.fromCharCode(...new Uint8Array(event.data, 0, 4));
        if (tag === 'AUDS') {
          if (playState().system) {
            const pcm = event.data.slice(4);
            // 静音段推进时间轴但不发声：直接丢弃会造成时间轴漂移与调度重置
            // 爆音，播放静音段又浪费资源——推进时间轴是最优处理
            if (isAllZeroPCM(pcm)) skipPCM(pcm, 'sys');
            else playPCM(pcm, 'sys');
          }
          return;
        } else if (tag === 'AUDM') {
          if (playState().mic) {
            const pcm = event.data.slice(4);
            if (isAllZeroPCM(pcm)) skipPCM(pcm, 'mic');
            else playPCM(pcm, 'mic');
          }
          return;
        } else if (tag === 'AUPS') {
          if (playState().system) decodeOpus(event.data.slice(4), 'sys');
          return;
        } else if (tag === 'AUPM') {
          if (playState().mic) decodeOpus(event.data.slice(4), 'mic');
          return;
        }
      }
      if (playState().system || playState().mic) {
        playPCM(event.data, playState().system ? 'sys' : 'mic');
      }
      return;
    }
    if (typeof event.data === 'string') {
      try {
        const msg = JSON.parse(event.data);
        if (msg.type === 'mouse' && msg.peer_id) {
          const posX = Math.round(msg.x * (msg.screen_w || 1920));
          const posY = Math.round(msg.y * (msg.screen_h || 1080));
          telemetry.cursor[msg.peer_id] = '● 正在本地执行: (' + posX + ', ' + posY + ')';
        } else if (msg.type === 'keyboard' && msg.peer_id) {
          const actionText = msg.action === 'down' ? '按下' : '释放';
          const keyName = msg.friendly || msg.key || '按键';
          telemetry.keyboard[msg.peer_id] = '● 正在本地执行: ' + actionText + ' [' + keyName + ']';
        }
      } catch {
        // 忽略非 JSON 文本帧
      }
    }
  };

  ws.onclose = (e) => {
    connState.wsOpen = false;
    report('ws_close', 'code=' + (e && e.code) + ' reason=' + (e && e.reason) + ' wasClean=' + (e && e.wasClean));
    // 重连后服务端订阅回到默认值：重置已发送标记，onopen 时重新同步
    lastSubs = { audio: null, mouse: null, keyboard: null };
    reconnectTimer = setTimeout(connectWebSocket, 2000);
  };
}

// 播放对端音频前先解锁 AudioContext
// resumeAudio 供"点击恢复播放"按钮调用：用户手势内恢复被系统挂起的 AudioContext
export function resumeAudio() {
  ensureAudioContext();
  audioPlayState.text = 'ctx=' + audioContextState();
  setTimeout(() => { audioPlayState.text = 'ctx=' + audioContextState(); }, 600);
}

export function primeAudio() {
  ensureAudioContext();
  // 播放链路状态对外可见：AudioContext 是否被系统挂起（无声排查关键线索）
  audioPlayState.text = 'ctx=' + audioContextState();
}

// —— 映射与改名操作 ——
export async function toggleMapping(peer, field, nextState, toastOn, toastOff) {
  try {
    await api.updateMapping(peer.id, { [field]: nextState });
    showToast(nextState ? toastOn : toastOff, nextState ? 'success' : 'info');
    await fetchPeers();
  } catch (e) {
    showToast('映射设置失败: ' + ((e && e.message) || e), 'error');
  }
}

// 改名走应用内弹窗：window.prompt/alert 无法随深色主题定制，
// 在原生窗口（Wails/WKWebView）中还会脱离页面视觉与焦点链路。
export function openRenameSelf() {
  const name = selfState.data ? selfState.data.name : '';
  uiState.rename = { kind: 'self', id: '', currentName: name, value: name };
  uiState.modal = 'rename';
}

export function openRenamePeer(peer) {
  const name = (peer && peer.name) || '';
  uiState.rename = { kind: 'peer', id: peer.id, currentName: name, value: name };
  uiState.modal = 'rename';
}

export async function submitRename() {
  const r = uiState.rename;
  if (!r) return;
  const trimmed = String(r.value || '').trim();
  if (!trimmed) {
    showToast('名称不能为空', 'error');
    return;
  }
  if (trimmed === r.currentName) {
    closeModal();
    return;
  }
  try {
    if (r.kind === 'self') {
      await api.renameSelf(trimmed);
      await fetchSelf();
      showToast('本机名称已更新为 ' + trimmed, 'success');
    } else {
      await api.renamePeer(r.id, trimmed);
      await fetchPeers();
      showToast('对端备注已更新为 ' + trimmed, 'success');
    }
    closeModal();
  } catch (e) {
    showToast('修改名称失败: ' + ((e && e.message) || e), 'error');
  }
}

export async function switchNic(ip) {
  if (!ip) return;
  try {
    await api.selectNic(ip);
    showToast('已切换通信网卡至 ' + ip, 'success');
    await fetchSelf();
  } catch (e) {
    showToast('切换网卡失败: ' + ((e && e.message) || e), 'error');
  }
}

// 添加对端：返回结果对象由弹窗内联展示错误（不再用 alert 阻塞浏览器）
export async function addPeer(target, name) {
  if (!target) {
    return { ok: false, error: '请输入对端的 IP:端口，例如 192.168.1.100:8080' };
  }
  try {
    await api.addPeer(target, name);
    await fetchPeers();
    showToast('已添加对端 ' + target, 'success');
    return { ok: true };
  } catch (e) {
    return { ok: false, error: String((e && e.message) || e) };
  }
}

export async function saveSelfSettings(payload) {
  await api.updateSelfProvides(payload);
  closeModal();
  await fetchSelf();
  showToast('本机共享设置已更新', 'success');
}

// —— 生命周期：轮询定时器（供组件 onMount/onDestroy 调用） ——
export function startPolling() {
  fetchSelf().then(fetchPeers);
  connectWebSocket();
  const peersTimer = setInterval(fetchPeers, 1200);
  const selfTimer = setInterval(fetchSelf, 5000);
  return () => {
    clearInterval(peersTimer);
    clearInterval(selfTimer);
    clearTimeout(reconnectTimer);
    if (ws) ws.close();
  };
}
