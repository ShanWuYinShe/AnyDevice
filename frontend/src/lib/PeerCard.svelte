<script>
  import { telemetry, renamePeer, toggleMapping, primeAudio } from './state.svelte.js';

  let { peer } = $props();

  const mapping = $derived(peer.mapping || {});
  const provides = $derived(peer.provides || {});

  const hasActiveMapping = $derived(
    mapping.play_system_audio_local || mapping.play_mic_audio_local ||
    mapping.play_audio_local || mapping.execute_mouse_local || mapping.execute_keyboard_local
  );
  const devIcon = $derived(
    peer.os === 'iOS' || peer.os === 'Android' ? '📱'
      : peer.os === 'macOS' ? '🍏'
      : peer.os === 'Windows' ? '🪟' : '💻'
  );
  const hasSystemAudio = $derived(!!(provides.system_audio || provides.audio));
  const hasMicAudio = $derived(!!provides.mic_audio);
  const hasMouse = $derived(!!provides.mouse);
  const hasKeyboard = $derived(!!provides.keyboard);
  const sysAudioMapped = $derived(!!(mapping.play_system_audio_local || mapping.play_audio_local));
  const micAudioMapped = $derived(!!mapping.play_mic_audio_local);
  const mouseMapped = $derived(!!mapping.execute_mouse_local);
  const keyboardMapped = $derived(!!mapping.execute_keyboard_local);

  const cursorText = $derived(telemetry.cursor[peer.id] || '● 正在本地执行对端物理鼠标');
  const keyboardText = $derived(telemetry.keyboard[peer.id] || '● 正在本地执行对端物理按键');

  function onToggleSystemAudio() {
    primeAudio();
    const next = !sysAudioMapped;
    // 开启时带上设备名：若对端是占位测试音（未装虚拟声卡）用户能立即识别
    const dev = provides.audio_device ? '（' + provides.audio_device + '）' : '';
    toggleMapping(peer, 'play_system_audio', next,
      '已开启播放对端系统声音 🔊 ' + dev, '已停止播放系统声音 🔇');
  }
  function onToggleMicAudio() {
    primeAudio();
    const next = !micAudioMapped;
    const dev = provides.mic_device ? '（' + provides.mic_device + '）' : '';
    toggleMapping(peer, 'play_mic_audio', next,
      '已开启收听对端麦克风 🎙️ ' + dev, '已停止收听麦克风 🔇');
  }
  function onToggleMouse() {
    const next = !mouseMapped;
    toggleMapping(peer, 'execute_mouse', next,
      '已开启同步对端物理鼠标 🖱️', '已停止同步对端鼠标 ⏹️');
  }
  function onToggleKeyboard() {
    const next = !keyboardMapped;
    toggleMapping(peer, 'execute_keyboard', next,
      '已开启同步对端物理键盘 ⌨️', '已停止同步对端键盘 ⏹️');
  }
</script>

<div class="peer-card" class:active-mapping={hasActiveMapping}>
  <div class="peer-header">
    <div class="peer-main-info">
      <div class="peer-avatar">{devIcon}</div>
      <div class="peer-names">
        <div class="peer-name-row">
          <span class="peer-name">{peer.name}</span>
          <button class="rename-trigger-btn" title="修改此对端的备注名称" onclick={() => renamePeer(peer, peer.name)}>✏️ 备注</button>
        </div>
        <div class="peer-addr">{peer.ip}:{peer.port} · 最近更新 {peer.last_seen || ''}</div>
      </div>
    </div>
    <div class="peer-status-pill" class:online={peer.online} class:offline={!peer.online}>
      {peer.online ? '● 在线' : '○ 离线'}
    </div>
  </div>

  <div class="devices-section">
    <div class="devices-section-title">该对端提供的设备与服务 (点击自主选择映射为本地设备)</div>

    <!-- 系统声音输出 -->
    <div class="service-row">
      <div class="service-info">
        <div class="service-label"><span>🎵</span><span>系统声音输出 (扬声器原声)</span></div>
        <span class="service-state-text">
          {hasSystemAudio ? (provides.audio_device || '立体声系统声音流') : '对端未开启系统声音'}
        </span>
      </div>
      <button class="map-btn" class:active-emerald={sysAudioMapped} disabled={!hasSystemAudio} onclick={onToggleSystemAudio}>
        {sysAudioMapped ? '🔊 正在本地播放对端系统声音 (点击关闭)' : '🔈 映射并在本地播放系统声音'}
      </button>
    </div>

    <!-- 麦克风输入 -->
    <div class="service-row">
      <div class="service-info">
        <div class="service-label"><span>🎙️</span><span>麦克风音频输入 (拾音)</span></div>
        <span class="service-state-text">
          {hasMicAudio ? (provides.mic_device || '麦克风高清拾音') : '对端未开启麦克风'}
        </span>
      </div>
      <button class="map-btn" class:active-purple={micAudioMapped} disabled={!hasMicAudio} onclick={onToggleMicAudio}>
        {micAudioMapped ? '🎙️ 正在本地收听对端麦克风 (点击关闭)' : '🎙️ 映射并在本地收听麦克风'}
      </button>
    </div>

    <!-- 物理鼠标同步 -->
    <div class="service-row">
      <div class="service-info">
        <div class="service-label"><span>🖱️</span><span>物理鼠标设备 (同步操作)</span></div>
        <span class="service-state-text">{hasMouse ? '物理鼠标实时同步' : '对端未提供鼠标'}</span>
      </div>
      <button class="map-btn" class:active-blue={mouseMapped} disabled={!hasMouse} onclick={onToggleMouse}>
        {mouseMapped ? '🖱️ 正在同步对端鼠标 (点击停止)' : '🖱️ 映射为本地鼠标 (同步对端鼠标)'}
      </button>
      {#if mouseMapped}
        <div class="cursor-telemetry">{cursorText}</div>
      {/if}
    </div>

    <!-- 物理键盘同步 -->
    <div class="service-row">
      <div class="service-info">
        <div class="service-label"><span>⌨️</span><span>物理键盘设备 (同步操作)</span></div>
        <span class="service-state-text">{hasKeyboard ? '物理按键直通同步' : '对端未提供键盘'}</span>
      </div>
      <button class="map-btn" class:active-amber={keyboardMapped} disabled={!hasKeyboard} onclick={onToggleKeyboard}>
        {keyboardMapped ? '⌨️ 正在同步对端键盘 (点击停止)' : '⌨️ 映射为本地键盘 (同步对端按键)'}
      </button>
      {#if keyboardMapped}
        <div class="keyboard-telemetry">{keyboardText}</div>
      {/if}
    </div>
  </div>
</div>
