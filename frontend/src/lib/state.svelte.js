// 全局响应式状态（Svelte 5 runes 模块）：数据轮询、WebSocket 流、订阅同步
import { api } from './api.js';
import { ensureAudioContext, playPCM } from './audio.js';

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

export function connectWebSocket() {
  const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
  ws = new WebSocket(proto + '//' + location.host + '/ws');
  ws.binaryType = 'arraybuffer';

  ws.onopen = () => {
    connState.wsOpen = true;
    // 连接建立即同步当前订阅（服务端对本机客户端默认不订阅）
    sendSubscriptions();
  };

  ws.onmessage = (event) => {
    if (event.data instanceof ArrayBuffer) {
      if (event.data.byteLength > 4) {
        const tag = String.fromCharCode(...new Uint8Array(event.data, 0, 4));
        if (tag === 'AUDS') {
          if (playState().system) playPCM(event.data.slice(4));
          return;
        } else if (tag === 'AUDM') {
          if (playState().mic) playPCM(event.data.slice(4));
          return;
        }
      }
      if (playState().system || playState().mic) {
        playPCM(event.data);
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

  ws.onclose = () => {
    connState.wsOpen = false;
    // 重连后服务端订阅回到默认值：重置已发送标记，onopen 时重新同步
    lastSubs = { audio: null, mouse: null, keyboard: null };
    reconnectTimer = setTimeout(connectWebSocket, 2000);
  };
}

// 播放对端音频前先解锁 AudioContext
export function primeAudio() {
  ensureAudioContext();
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
