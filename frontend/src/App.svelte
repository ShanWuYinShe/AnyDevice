<script>
  import { onMount } from 'svelte';
  import {
    selfState, peerState, uiState, targetSubs,
    openModal, closeModal, startPolling, sendSubscriptions,
    fetchPeers, renameSelf, switchNic, addPeer, saveSelfSettings, showToast,
  } from './lib/state.svelte.js';
  import PeerCard from './lib/PeerCard.svelte';
  import Modal from './lib/Modal.svelte';

  // 网卡下拉当前选中值：本机信息载入后对齐 current_ip
  let selectedNic = $state('');
  $effect(() => {
    if (selfState.data && selfState.data.current_ip) {
      selectedNic = selfState.data.current_ip;
    }
  });

  // 目标订阅组合变化且 WS 就绪时同步给服务端
  $effect(() => {
    void targetSubs();
    sendSubscriptions();
  });

  // 本机共享设置表单状态
  let selfNameInput = $state('');
  let selfSystemAudio = $state(true);
  let selfMicAudio = $state(true);
  let selfMouse = $state(true);
  let selfKeyboard = $state(true);

  function openSelfSettings() {
    const d = selfState.data;
    if (d) {
      selfNameInput = d.name || '';
      selfSystemAudio = !!(d.provides && (d.provides.system_audio || d.provides.audio));
      selfMicAudio = !!(d.provides && d.provides.mic_audio);
      selfMouse = !!(d.provides && d.provides.mouse);
      selfKeyboard = !!(d.provides && d.provides.keyboard);
    }
    openModal('self');
  }

  function submitSelfSettings() {
    saveSelfSettings({
      name: selfNameInput.trim(),
      system_audio: selfSystemAudio,
      mic_audio: selfMicAudio,
      mouse: selfMouse,
      keyboard: selfKeyboard,
    });
  }

  // 手动添加对端表单
  let manualTarget = $state('');
  let manualName = $state('');
  let addingPeer = $state(false);

  async function submitAddPeer() {
    addingPeer = true;
    try {
      const ok = await addPeer(manualTarget.trim(), manualName.trim());
      if (ok) {
        manualTarget = '';
        manualName = '';
      }
    } finally {
      addingPeer = false;
    }
  }

  function refreshQR() {
    const img = document.getElementById('qrCodeImg');
    if (img) img.src = '/api/qrcode?t=' + Date.now();
  }

  function copyUrl() {
    if (selfState.data && selfState.data.primary_url) {
      navigator.clipboard.writeText(selfState.data.primary_url);
      showToast('已复制访问地址到剪贴板 📋');
    }
  }

  onMount(() => startPolling());
</script>

<header>
  <div class="header-left">
    <div class="logo"><span>🔄 AnyDevice</span></div>
    <div class="host-badge">
      <span class="dot"></span>
      <span>{selfState.data ? selfState.data.name + ' (' + selfState.data.current_ip + ')' : '本机设备'}</span>
      <button class="rename-trigger-btn" title="修改本机名称" onclick={renameSelf}>✏️ 改名</button>
    </div>
    <select class="nic-select" title="切换通信网卡" bind:value={selectedNic} onchange={() => switchNic(selectedNic)}>
      {#if selfState.data && selfState.data.interfaces}
        {#each selfState.data.interfaces as iface (iface.name + '|' + iface.ip)}
          <option value={iface.ip}>{iface.name} - {iface.ip}</option>
        {/each}
      {:else}
        <option value="">载入网卡中...</option>
      {/if}
    </select>
  </div>
  <div class="header-right">
    <button class="nav-btn" onclick={() => { refreshQR(); openModal('qr'); }}>📱 手机扫码接入</button>
    <button class="nav-btn" onclick={() => openModal('add')}>➕ 手动添加对端</button>
    <button class="nav-btn primary" onclick={openSelfSettings}>⚙️ 本机共享功能</button>
  </div>
</header>

<main>
  <div class="discovery-header">
    <div class="discovery-title">
      <h1>发现的对端设备</h1>
      <div class="discovery-status-tag">
        <span class="pulse-dot"></span>
        <span>发现 {peerState.list.length} 台对端设备</span>
      </div>
    </div>
    <button class="nav-btn" onclick={fetchPeers}>🔄 刷新设备列表</button>
  </div>

  <p class="discovery-hint">
    每个端点设备会提供各自的声音输出、麦克风录音、鼠标光标或物理键盘。您可以在下方决定是否将对端的功能映射为本地播放或操作。
  </p>

  <div class="peer-grid">
    {#if peerState.list.length === 0}
      <div class="empty-state">
        <div class="empty-icon">📡</div>
        <h3>正在自动搜索局域网中的 AnyDevice 对端...</h3>
        <p>
          请在同一局域网下的其他电脑打开 AnyDevice，或者点击右上角「📱 手机扫码接入」。设备发现后将即刻呈现在这里。
        </p>
        <div class="empty-actions">
          <button class="nav-btn primary" onclick={() => openModal('add')}>➕ 手动输入 IP 连接</button>
          <button class="nav-btn" onclick={() => { refreshQR(); openModal('qr'); }}>📱 扫码连接</button>
        </div>
      </div>
    {:else}
      {#each peerState.list as p (p.id)}
        <PeerCard peer={p} />
      {/each}
    {/if}
  </div>
</main>

<!-- 页脚：构建版本与日志路径（确认运行的是哪个版本、日志去哪找） -->
<footer style="padding: 14px 20px 22px; text-align: center; font-size: 0.72rem; color: var(--text-sub); border-top: 1px solid var(--border); margin-top: 8px;">
  <div>
    构建: {selfState.data && selfState.data.build_time ? selfState.data.build_time : 'unknown（旧版本）'}
    {#if selfState.data && selfState.data.build_time && selfState.data.build_time !== 'dev'}
      ✓ 最新版
    {/if}
  </div>
  {#if selfState.data && selfState.data.log_path}
    <div style="margin-top: 4px; word-break: break-all;">
      日志文件: <span style="color: #60a5fa;">{selfState.data.log_path}</span>
    </div>
  {/if}
</footer>

<!-- 弹窗：本机共享功能设置 -->
<Modal open={uiState.modal === 'self'} title="⚙️ 本机功能共享设置" onClose={closeModal}>
  <p style="font-size: 0.82rem; color: var(--text-muted); line-height: 1.4;">
    配置本机向局域网其他设备开放提供的功能与虚拟设备。对端发现本机后，可选择是否映射使用。
  </p>
  <div class="form-group">
    <label for="selfNameInput">本机设备名称 (局域网显示名称)</label>
    <input class="form-input" id="selfNameInput" type="text" bind:value={selfNameInput} placeholder="例如: 我的 MacBook Pro" />
  </div>
  <div class="switch-row">
    <div class="switch-label">
      <span class="switch-title">🎵 共享本机系统声音输出</span>
      <span class="switch-desc">允许对端在他们电脑上收听播放本机的系统声音 (扬声器输出)</span>
    </div>
    <label class="toggle-switch">
      <input type="checkbox" bind:checked={selfSystemAudio} />
      <span class="slider"></span>
    </label>
  </div>
  <div class="switch-row">
    <div class="switch-label">
      <span class="switch-title">🎙️ 共享本机麦克风音频输入</span>
      <span class="switch-desc">允许对端在他们电脑上收听本机的麦克风录音</span>
    </div>
    <label class="toggle-switch">
      <input type="checkbox" bind:checked={selfMicAudio} />
      <span class="slider"></span>
    </label>
  </div>
  <div class="switch-row">
    <div class="switch-label">
      <span class="switch-title">🖱️ 共享本机物理鼠标操作</span>
      <span class="switch-desc">允许对端在他们电脑上实时同步执行本机的鼠标光标与按键</span>
    </div>
    <label class="toggle-switch">
      <input type="checkbox" bind:checked={selfMouse} />
      <span class="slider"></span>
    </label>
  </div>
  <div class="switch-row">
    <div class="switch-label">
      <span class="switch-title">⌨️ 共享本机物理键盘操作</span>
      <span class="switch-desc">允许对端在他们电脑上实时同步执行本机的键盘按键 (无弹窗直通)</span>
    </div>
    <label class="toggle-switch">
      <input type="checkbox" bind:checked={selfKeyboard} />
      <span class="slider"></span>
    </label>
  </div>
  <button class="nav-btn primary" style="padding: 10px; justify-content: center;" onclick={submitSelfSettings}>
    保存设置
  </button>
</Modal>

<!-- 弹窗：扫码接入 -->
<Modal open={uiState.modal === 'qr'} title="📱 手机 / 平板扫码接入" onClose={closeModal}>
  <p style="font-size: 0.82rem; color: var(--text-muted); line-height: 1.4;">
    用手机相机或微信扫码打开下方链接，手机即可作为对端自动加入列表。
  </p>
  <div class="qr-wrapper">
    <img id="qrCodeImg" src="/api/qrcode" alt="二维码" />
    <div class="qr-url-box">{selfState.data ? selfState.data.primary_url : 'http://...'}</div>
  </div>
  <button class="nav-btn primary" style="padding: 10px; justify-content: center;" onclick={copyUrl}>
    📋 复制访问地址
  </button>
</Modal>

<!-- 弹窗：手动添加对端 -->
<Modal open={uiState.modal === 'add'} title="➕ 手动添加对端设备" onClose={closeModal}>
  <p style="font-size: 0.82rem; color: var(--text-muted); line-height: 1.4;">
    如果局域网禁用了广播自动发现，可以直接输入其他电脑的 IP:端口进行连接。
  </p>
  <div class="form-group">
    <label for="manualTargetInput">对端 IP 地址与端口</label>
    <input class="form-input" id="manualTargetInput" type="text" bind:value={manualTarget} placeholder="例如 192.168.1.100:8080" />
  </div>
  <div class="form-group">
    <label for="manualNameInput">设备备注名称 (可选)</label>
    <input class="form-input" id="manualNameInput" type="text" bind:value={manualName} placeholder="例如: 办公室台式机" />
  </div>
  <button class="nav-btn primary" style="padding: 10px; justify-content: center;" disabled={addingPeer} onclick={submitAddPeer}>
    {addingPeer ? '连接中...' : '确认添加并搜索设备'}
  </button>
</Modal>

<!-- Toast 提示 -->
<div class="toast" class:show={uiState.toast !== ''}>{uiState.toast}</div>
