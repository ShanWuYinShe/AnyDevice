<script>
  import { onMount } from 'svelte';
  import {
    selfState, peerState, uiState, connState, targetSubs,
    openModal, closeModal, startPolling, sendSubscriptions,
    fetchPeers, openRenameSelf, submitRename, switchNic,
    addPeer, saveSelfSettings, showToast,
  } from './lib/state.svelte.js';
  import PeerCard from './lib/PeerCard.svelte';
  import Modal from './lib/Modal.svelte';
  import Icon from './lib/Icon.svelte';
  import NicSelect from './lib/NicSelect.svelte';

  const self = $derived(selfState.data || {});
  const selfOs = $derived(
    self.os === 'darwin' ? 'macOS'
      : self.os === 'windows' ? 'Windows'
      : self.os === 'linux' ? 'Linux'
      : self.os || '未知系统'
  );

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
  let addError = $state('');

  async function submitAddPeer() {
    addError = '';
    addingPeer = true;
    try {
      const res = await addPeer(manualTarget.trim(), manualName.trim());
      if (res.ok) {
        manualTarget = '';
        manualName = '';
        closeModal();
      } else {
        addError = res.error;
      }
    } finally {
      addingPeer = false;
    }
  }

  function refreshQR() {
    const img = document.getElementById('qrCodeImg');
    if (img) img.src = '/api/qrcode?t=' + Date.now();
  }

  async function copyUrl() {
    const url = self.primary_url;
    if (!url) {
      showToast('尚未获取到局域网访问地址', 'error');
      return;
    }
    try {
      await navigator.clipboard.writeText(url);
      showToast('已复制访问地址', 'success');
      return;
    } catch {
      // 原生窗口/无剪贴板权限时的回退路径
      const el = document.createElement('textarea');
      el.value = url;
      el.setAttribute('readonly', '');
      el.style.position = 'fixed';
      el.style.opacity = '0';
      document.body.appendChild(el);
      el.select();
      try {
        document.execCommand('copy');
        showToast('已复制访问地址', 'success');
      } catch {
        showToast('复制失败，请手动选择地址复制', 'error');
      }
      el.remove();
    }
  }

  onMount(() => startPolling());
</script>

<header class="app-header">
  <div class="brand">
    <div class="brand-mark">
      <img src="/icon-32.png" alt="AnyDevice" class="brand-icon-img" />
    </div>
    <div class="brand-text">
      <span class="brand-name">AnyDevice</span>
      <span class="brand-sub">跨端设备映射中枢</span>
    </div>
  </div>
  <div class="header-actions">
    <button
      class="btn"
      title="手机扫码接入"
      aria-label="手机扫码接入"
      onclick={() => { refreshQR(); openModal('qr'); }}
    >
      <Icon name="qr" size={17} /><span class="btn-label">扫码接入</span>
    </button>
    <button
      class="btn"
      title="手动添加对端设备"
      aria-label="手动添加对端设备"
      onclick={() => openModal('add')}
    >
      <Icon name="plus" size={17} /><span class="btn-label">添加设备</span>
    </button>
    <button
      class="btn btn-primary"
      title="本机共享功能设置"
      aria-label="本机共享功能设置"
      onclick={openSelfSettings}
    >
      <Icon name="sliders" size={17} /><span class="btn-label">本机共享</span>
    </button>
  </div>
</header>

<main>
  <section class="overview" aria-label="本机与通道状态">
    <div class="ov-cell">
      <div class="ov-icon"><Icon name="laptop" size={19} /></div>
      <div class="ov-body">
        <div class="ov-label">本机设备</div>
        <div class="ov-value-row">
          <span class="ov-value" title={self.name || '本机设备'}>{self.name || '本机设备'}</span>
          <button class="icon-btn icon-btn-xs" title="修改本机名称" aria-label="修改本机名称" onclick={openRenameSelf}>
            <Icon name="pencil" size={13} />
          </button>
        </div>
        <div class="ov-meta mono">{selfOs} · {self.current_ip || '未获取 IP'}</div>
      </div>
    </div>

    <div class="ov-cell ov-cell-nic">
      <div class="ov-icon"><Icon name="wifi" size={19} /></div>
      <div class="ov-body ov-body-grow">
        <span class="ov-label">网络通道</span>
        <NicSelect
          interfaces={self.interfaces || []}
          value={selectedNic}
          onchange={(newIp) => {
            selectedNic = newIp;
            switchNic(newIp);
          }}
        />
      </div>
    </div>

    <div class="ov-cell">
      <div class="ov-icon"><Icon name="link" size={19} /></div>
      <div class="ov-body">
        <div class="ov-label">实时通道</div>
        <div class="ov-value-row">
          <span class="status-dot" class:is-live={connState.wsOpen} class:is-warn={!connState.wsOpen} aria-hidden="true"></span>
          <span class="ov-value">{connState.wsOpen ? '已连接' : '重连中…'}</span>
        </div>
        <div class="ov-meta">音频与遥测数据链路</div>
      </div>
    </div>
  </section>

  <div class="section-head">
    <div class="section-title">
      <h1>对端设备</h1>
      <span class="count-pill">{peerState.list.length} 台</span>
    </div>
    <button class="icon-btn" title="立即刷新设备列表" aria-label="立即刷新设备列表" onclick={fetchPeers}>
      <Icon name="refresh" size={17} />
    </button>
  </div>
  <p class="section-hint">下面是局域网中发现的对端。逐项选择要把对端的哪种能力映射到本机，高亮表示该能力正在本机生效。</p>

  <div class="peer-grid">
    {#if peerState.list.length === 0}
      <div class="empty-state">
        <div class="radar" aria-hidden="true">
          <span class="radar-ring"></span>
          <span class="radar-ring"></span>
          <Icon name="wifi" size={26} />
        </div>
        <h2 class="empty-title">正在搜索局域网设备</h2>
        <p class="empty-text">
          请确认另一台设备已打开 AnyDevice，并与本机连接到同一网络。设备上线后会立即出现在这里；
          若所在网络禁用了广播发现，可手动输入对端 IP 连接。
        </p>
        <div class="empty-actions">
          <button class="btn btn-primary" onclick={() => { refreshQR(); openModal('qr'); }}>
            <Icon name="qr" size={16} /><span>扫码接入</span>
          </button>
          <button class="btn" onclick={() => openModal('add')}>
            <Icon name="plus" size={16} /><span>手动输入 IP</span>
          </button>
        </div>
      </div>
    {:else}
      {#each peerState.list as p (p.id)}
        <PeerCard peer={p} />
      {/each}
    {/if}
  </div>
</main>

<footer class="app-footer">
  <span class="footer-item">
    <Icon name="checkCircle" size={13} />
    运行版本 {self.build_time || 'unknown（旧版本）'}
  </span>
  {#if self.log_path}
    <span class="footer-item">
      日志
      <span class="footer-mono" title={self.log_path}>{self.log_path}</span>
    </span>
  {/if}
</footer>

<!-- 弹窗：本机共享功能设置 -->
<Modal open={uiState.modal === 'self'} title="本机共享设置" onClose={closeModal}>
  <p class="modal-desc">配置本机向局域网其他设备开放的能力。对端发现本机后，可自行选择是否映射使用。</p>
  <div class="form-group">
    <label class="form-label" for="selfNameInput">本机设备名称</label>
    <input
      class="form-input"
      id="selfNameInput"
      type="text"
      bind:value={selfNameInput}
      placeholder="例如：我的 MacBook Pro"
      data-autofocus
    />
  </div>
  <div class="switch-list">
    <div class="switch-row">
      <div class="switch-info">
        <div class="switch-icon"><Icon name="speaker" size={18} /></div>
        <div>
          <div class="switch-title">共享系统声音输出</div>
          <div class="switch-desc">允许对端收听本机扬声器输出的原声</div>
        </div>
      </div>
      <label class="toggle-switch">
        <input type="checkbox" bind:checked={selfSystemAudio} aria-label="共享本机系统声音输出" />
        <span class="slider"></span>
      </label>
    </div>
    <div class="switch-row">
      <div class="switch-info">
        <div class="switch-icon"><Icon name="mic" size={18} /></div>
        <div>
          <div class="switch-title">共享麦克风音频输入</div>
          <div class="switch-desc">允许对端收听本机的麦克风录音</div>
        </div>
      </div>
      <label class="toggle-switch">
        <input type="checkbox" bind:checked={selfMicAudio} aria-label="共享本机麦克风音频输入" />
        <span class="slider"></span>
      </label>
    </div>
    <div class="switch-row">
      <div class="switch-info">
        <div class="switch-icon"><Icon name="mouse" size={18} /></div>
        <div>
          <div class="switch-title">共享物理鼠标操作</div>
          <div class="switch-desc">允许对端同步执行本机的光标移动与按键</div>
        </div>
      </div>
      <label class="toggle-switch">
        <input type="checkbox" bind:checked={selfMouse} aria-label="共享本机物理鼠标操作" />
        <span class="slider"></span>
      </label>
    </div>
    <div class="switch-row">
      <div class="switch-info">
        <div class="switch-icon"><Icon name="keyboard" size={18} /></div>
        <div>
          <div class="switch-title">共享物理键盘操作</div>
          <div class="switch-desc">允许对端同步执行本机的键盘按键</div>
        </div>
      </div>
      <label class="toggle-switch">
        <input type="checkbox" bind:checked={selfKeyboard} aria-label="共享本机物理键盘操作" />
        <span class="slider"></span>
      </label>
    </div>
  </div>
  <button class="btn btn-primary btn-block" onclick={submitSelfSettings}>保存设置</button>
</Modal>

<!-- 弹窗：扫码接入 -->
<Modal open={uiState.modal === 'qr'} title="手机 / 平板扫码接入" onClose={closeModal}>
  <p class="modal-desc">用手机相机扫码打开链接，手机即作为对端自动加入设备列表。</p>
  <div class="qr-box">
    <img class="qr-image" id="qrCodeImg" src="/api/qrcode" alt="接入地址二维码" />
    <div class="qr-url mono">{self.primary_url || '未获取到局域网地址'}</div>
  </div>
  <button class="btn btn-primary btn-block" onclick={copyUrl}>
    <Icon name="copy" size={16} /><span>复制访问地址</span>
  </button>
</Modal>

<!-- 弹窗：手动添加对端 -->
<Modal open={uiState.modal === 'add'} title="手动添加对端设备" onClose={closeModal}>
  <p class="modal-desc">局域网禁用广播自动发现时，可直接输入对端 IP:端口 建立连接。</p>
  <div class="form-group">
    <label class="form-label" for="manualTargetInput">对端 IP 与端口</label>
    <input
      class="form-input"
      id="manualTargetInput"
      type="text"
      bind:value={manualTarget}
      placeholder="例如 192.168.1.100:8080"
      data-autofocus
    />
  </div>
  <div class="form-group">
    <label class="form-label" for="manualNameInput">设备备注名称（可选）</label>
    <input class="form-input" id="manualNameInput" type="text" bind:value={manualName} placeholder="例如：办公室台式机" />
  </div>
  {#if addError}
    <div class="form-error" role="alert">{addError}</div>
  {/if}
  <button class="btn btn-primary btn-block" disabled={addingPeer} onclick={submitAddPeer}>
    {addingPeer ? '连接中…' : '确认添加'}
  </button>
</Modal>

<!-- 弹窗：改名 / 备注 -->
<Modal
  open={uiState.modal === 'rename'}
  title={uiState.rename && uiState.rename.kind === 'self' ? '修改本机名称' : '修改对端备注'}
  onClose={closeModal}
>
  {#if uiState.rename}
    <p class="modal-desc">
      {uiState.rename.kind === 'self'
        ? '本机名称会保存到配置文件，并显示在局域网内其他设备的列表中。'
        : '备注名称只在本机显示，优先于对端广播的名称，不会被后续发现心跳覆盖。'}
    </p>
    <div class="form-group">
      <label class="form-label" for="renameInput">{uiState.rename.kind === 'self' ? '本机名称' : '备注名称'}</label>
      <input
        class="form-input"
        id="renameInput"
        type="text"
        bind:value={uiState.rename.value}
        placeholder={uiState.rename.kind === 'self' ? '例如：我的 MacBook Pro' : '例如：办公室台式机'}
        data-autofocus
        onkeydown={(e) => { if (e.key === 'Enter') submitRename(); }}
      />
    </div>
    <div class="modal-actions">
      <button class="btn" onclick={closeModal}>取消</button>
      <button class="btn btn-primary" onclick={submitRename}>
        <Icon name="check" size={16} /><span>保存</span>
      </button>
    </div>
  {/if}
</Modal>

<!-- Toast 提示 -->
{#if uiState.toast}
  <div class="toast" class:is-error={uiState.toastType === 'error'} role="status" aria-live="polite">
    <Icon name={uiState.toastType === 'error' ? 'alert' : 'checkCircle'} size={16} />
    <span>{uiState.toast}</span>
  </div>
{/if}
