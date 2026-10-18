// 全局响应式状态（Svelte 5 runes 模块）：数据轮询、WebSocket 流、订阅同步
import { api } from './api.js';
import { ensureAudioContext, playPCM, audioContextState } from './audio.js';

// —— 可变状态（对象属性形式，跨模块保持响应式） ——
export const selfState = $state({ data: null }); // /api/self：本机信息
export const peerState = $state({ list: [] });   // /api/peers：已发现对端（服务端已过滤查看者自己）
export const uiState = $state({ toast: '', modal: '' }); // modal: '' | 'qr' | 'add' | 'self'
export const connState = $state({ wsOpen: false });
export const telemetry = $state({ cursor: {}, keyboard: {} }); // peerId -> 遥测文本

let toastTimer = null;
export function showToast(msg) {
  uiState.toast = msg;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => { uiState.toast = ''; }, 2500);
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
  ws.send(JSON.stringify({ action: 'set_mapping', audio: t.audio, mouse: t.mouse, keyboard: t.keyboard }));
  lastSubs = { audio: t.audio, mouse: t.mouse, keyboard: t.keyboard };
}

// —— WebSocket 音频与遥测流 ——
let ws = null;
let reconnectTimer = null;

// isAllZeroPCM 判断 16bit 交错 PCM 是否全零（静音段）。
// 全扫描 1920 个样本/50 帧每秒开销可忽略，避免误伤真实音频开头的静音采样。
function isAllZeroPCM(pcm) {
  const i16 = new Int16Array(pcm);
  for (let i = 0; i < i16.length; i++) {
    if (i16[i] !== 0) return false;
  }
  return true;
}

// 音频接收实时统计（卡片上可见）：按 tag 分类字节率（每 2 秒刷新一次）
export const audioRate = $state({ sys: 0, mic: 0 });
let audioRateBytes = { AUDS: 0, AUDM: 0 };
let audioRateLast = 0;
// 播放链路状态描述（AudioContext 是否被系统挂起等）
export const audioPlayState = $state({ text: '未创建' });

export function connectWebSocket() {
  // 浏览器访问：同源相对地址。
  // Wails 桌面窗口（location.host 为空）：必须用 127.0.0.1 回环地址——
  // WKWebView 的 ATS 安全策略会拦截 wails:// 页面对非 localhost 明文
  // WebSocket 的连接（此前用局域网 IP 推导导致 ws 永远连不上、音频无数据）。
  let wsBase;
  if (location.host) {
    const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
    wsBase = proto + '//' + location.host;
  } else {
    // 从 primary_url 仅提取端口，主机固定回环地址（ATS 豁免）
    let port = '8080';
    if (selfState.data && selfState.data.primary_url) {
      const m = selfState.data.primary_url.match(/:(\d+)$/);
      if (m) port = m[1];
    }
    wsBase = 'ws://127.0.0.1:' + port;
  }
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
        if (t === 'AUDS' || t === 'AUDM') audioRateBytes[t] = (audioRateBytes[t] || 0) + event.data.byteLength;
      }
      if (nowMs - audioRateLast >= 2000) {
        const kb = (n) => Math.round((audioRateBytes[n] || 0) / 2 / 1024);
        audioRate.sys = kb('AUDS');
        audioRate.mic = kb('AUDM');
        audioRateBytes = { AUDS: 0, AUDM: 0 };
        audioRateLast = nowMs;
      }
      if (event.data.byteLength > 4) {
        const tag = String.fromCharCode(...new Uint8Array(event.data, 0, 4));
        if (tag === 'AUDS') {
          if (playState().system) {
            const pcm = event.data.slice(4);
            // 丢弃全零静音段（采集端的 SILENT 补丁段）：静音 chunk 若照常插入
            // 播放队列，会形成 20ms 声音/20ms 静音的交替，听感为"嘟嘟"断续
            if (!isAllZeroPCM(pcm)) playPCM(pcm, 'sys');
          }
          return;
        } else if (tag === 'AUDM') {
          if (playState().mic) {
            const pcm = event.data.slice(4);
            if (!isAllZeroPCM(pcm)) playPCM(pcm, 'mic');
          }
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
    showToast(nextState ? toastOn : toastOff);
    await fetchPeers();
  } catch (e) {
    showToast('映射设置失败: ' + e);
  }
}

export async function renamePeer(peer, curName) {
  const newName = prompt('为设备 "' + curName + '" 设置备注名称:', curName);
  if (newName === null) return;
  const trimmed = newName.trim();
  if (trimmed === '' || trimmed === curName) return;
  try {
    await api.renamePeer(peer.id, trimmed);
    showToast('对端已备注为: ' + trimmed + ' ✏️');
    await fetchPeers();
  } catch (e) {
    showToast('修改备注失败: ' + e.message);
  }
}

export async function renameSelf() {
  const curName = selfState.data ? selfState.data.name : '';
  const newName = prompt('请输入新的本机设备名称 (在局域网中显示的名称):', curName);
  if (newName === null) return;
  const trimmed = newName.trim();
  if (trimmed === '' || trimmed === curName) return;
  try {
    await api.renameSelf(trimmed);
    showToast('本机名称已更新为: ' + trimmed + ' ✏️');
    await fetchSelf();
  } catch {
    showToast('修改本机名称失败');
  }
}

export async function switchNic(ip) {
  if (!ip) return;
  try {
    await api.selectNic(ip);
    showToast('已切换通信网卡 IP 至: ' + ip);
    await fetchSelf();
  } catch (e) {
    showToast('切换网卡失败: ' + e);
  }
}

export async function addPeer(target, name) {
  if (!target) {
    alert('请输入对端的 IP:端口，例如 192.168.1.100:8080');
    return false;
  }
  try {
    await api.addPeer(target, name);
    closeModal();
    await fetchPeers();
    showToast('已添加对端 ' + target);
    return true;
  } catch (e) {
    alert('添加失败: ' + e.message);
    return false;
  }
}

export async function saveSelfSettings(payload) {
  await api.updateSelfProvides(payload);
  closeModal();
  await fetchSelf();
  showToast('本机共享设置已更新');
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
