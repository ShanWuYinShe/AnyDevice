<script>
  import Icon from './Icon.svelte';
  import {
    telemetry, audioRate, audioPlayState,
    openRenamePeer, toggleMapping, primeAudio, resumeAudio,
  } from './state.svelte.js';

  let { peer } = $props();

  const mapping = $derived(peer.mapping || {});
  const provides = $derived(peer.provides || {});

  // 接收码率口径：服务端统计的「对端推流到本机」网络字节率（按 peer 独立）。
  // 不能用本窗口收到的字节数——原生窗口收到的是解码后的 float32 PCM
  // （约 375 KB/s），会把 Opus 的真实带宽（约 16 KB/s）放大 24 倍。
  // 旧服务端没有 rx_* 字段时退回本窗口自测值。
  const sysRateKBps = $derived(mapping.rx_system_audio_kbps ?? audioRate.sys);
  const micRateKBps = $derived(mapping.rx_mic_audio_kbps ?? audioRate.mic);
  const audioRateTitle =
    '实时接收码率：对端推流到本机的实际网络字节数（Opus 约 16 KB/s，旧版 PCM 约 350–380 KB/s）。' +
    '本机把 Opus 解码成 PCM 播放的部分不计入该数字。';

  const sysAudioMapped = $derived(!!(mapping.play_system_audio_local || mapping.play_audio_local));
  const micAudioMapped = $derived(!!mapping.play_mic_audio_local);
  const mouseMapped = $derived(!!mapping.execute_mouse_local);
  const keyboardMapped = $derived(!!mapping.execute_keyboard_local);
  const hasActiveMapping = $derived(
    sysAudioMapped || micAudioMapped || mouseMapped || keyboardMapped
  );

  const hasSystemAudio = $derived(!!(provides.system_audio || provides.audio));
  const hasMicAudio = $derived(!!provides.mic_audio);
  const hasMouse = $derived(!!provides.mouse);
  const hasKeyboard = $derived(!!provides.keyboard);

  // 协议防呆：只接受本前端能解码的音频载荷标识——
  //   opus-48000-stereo  ：当前协议（AUPS/AUPM，WebCodecs 解码）
  //   f32le-48000-stereo ：历史 float32 PCM（AUDS/AUDM，仍兼容）
  // 对端未广播 audio_format（更早的 int16 协议）或广播未知标识时禁止音频映射，
  // 否则解出来是纯噪声。
  const KNOWN_AUDIO_FORMATS = ['opus-48000-stereo', 'f32le-48000-stereo'];
  const audioFormatMismatch = $derived(
    !!peer.provides && !KNOWN_AUDIO_FORMATS.includes(String(peer.provides.audio_format || ''))
  );
  const audioFormatHint = $derived(
    '对端广播的音频协议为 ' + (String(peer.provides?.audio_format || '') || '（空，疑似更早的 int16 版本）')
  );

  const osLower = $derived(String(peer.os || '').toLowerCase());
  const osLabel = $derived(
    osLower === 'windows' ? 'Windows'
      : osLower === 'darwin' || osLower === 'macos' ? 'macOS'
      : osLower === 'ios' ? 'iOS'
      : osLower === 'android' ? 'Android'
      : osLower === 'linux' ? 'Linux'
      : peer.os || '未知系统'
  );
  const deviceIcon = $derived(
    osLower === 'ios' || osLower === 'android' ? 'phone'
      : osLower === 'windows' ? 'monitor'
      : 'laptop'
  );

  const audioSuspended = $derived(sysAudioMapped && String(audioPlayState.text).includes('suspended'));
  const connectionText = $derived(
    mapping.connection_status === 'connected' ? '已连接'
      : mapping.connection_status && mapping.connection_status !== 'disconnected'
        ? (String(mapping.connection_status).includes('error') ? '连接失败' : '连接中')
        : ''
  );

  const cursorText = $derived(String(telemetry.cursor[peer.id] || '').replace(/^●\s*/, ''));
  const keyboardText = $derived(String(telemetry.keyboard[peer.id] || '').replace(/^●\s*/, ''));

  const sysAudioTitle = $derived(sysAudioMapped ? '停止播放' : '映射播放');
  const micAudioTitle = $derived(micAudioMapped ? '停止收听' : '映射收听');
  const mouseTitle = $derived(mouseMapped ? '停止同步' : '映射同步');
  const keyboardTitle = $derived(keyboardMapped ? '停止同步' : '映射同步');

  function onToggleSystemAudio() {
    primeAudio();
    const next = !sysAudioMapped;
    // 开启时带上设备名：若对端是占位测试音（未装虚拟声卡）用户能立即识别
    const dev = provides.audio_device ? '（' + provides.audio_device + '）' : '';
    toggleMapping(peer, 'play_system_audio', next,
      '已开启播放对端系统声音 ' + dev, '已停止播放系统声音');
  }
  function onToggleMicAudio() {
    primeAudio();
    const next = !micAudioMapped;
    const dev = provides.mic_device ? '（' + provides.mic_device + '）' : '';
    toggleMapping(peer, 'play_mic_audio', next,
      '已开启收听对端麦克风 ' + dev, '已停止收听麦克风');
  }
  function onToggleMouse() {
    const next = !mouseMapped;
    toggleMapping(peer, 'execute_mouse', next,
      '已开启同步对端物理鼠标', '已停止同步对端鼠标');
  }
  function onToggleKeyboard() {
    const next = !keyboardMapped;
    toggleMapping(peer, 'execute_keyboard', next,
      '已开启同步对端物理键盘', '已停止同步对端键盘');
  }
</script>

<article class="peer-card" class:is-active={hasActiveMapping} class:is-offline={!peer.online} aria-label={'对端设备 ' + peer.name}>
  <header class="peer-head">
    <div class="peer-avatar" class:is-offline={!peer.online} title={osLabel}>
      <Icon name={deviceIcon} size={22} />
      <span class="online-dot" aria-hidden="true"></span>
    </div>
    <div class="peer-id">
      <div class="peer-name-row">
        <h3 class="peer-name" title={peer.name}>{peer.name}</h3>
        <span class="os-chip">{osLabel}</span>
        <button
          class="icon-btn icon-btn-xs"
          title="修改此对端的备注名称"
          aria-label={'修改 ' + peer.name + ' 的备注名称'}
          onclick={() => openRenamePeer(peer)}
        >
          <Icon name="pencil" size={13} />
        </button>
      </div>
      <div class="peer-meta">
        <span class="mono">{peer.ip}:{peer.port}</span>
        {#if peer.last_seen}
          <span class="meta-dot" aria-hidden="true"></span>
          <span title="最近一次心跳时间">更新于 {peer.last_seen}</span>
        {/if}
      </div>
    </div>
    <span class="peer-status-pill" class:online={peer.online} class:offline={!peer.online}>
      <span class="status-dot" class:is-live={peer.online} aria-hidden="true"></span>
      {peer.online ? '在线' : '离线'}
    </span>
  </header>

  <div class="cap-list">
    <!-- 系统声音输出 -->
    <div class="cap-row" class:is-on={sysAudioMapped} class:on-emerald={sysAudioMapped} class:is-warn={audioFormatMismatch}>
      <div class="cap-icon"><Icon name="speaker" size={19} /></div>
      <div class="cap-main">
        <div class="cap-title">
          <span>系统声音</span>
          {#if hasSystemAudio && provides.audio_device}
            <span class="cap-device" title={provides.audio_device}>{provides.audio_device}</span>
          {/if}
        </div>
        <div class="cap-desc" class:is-warn={audioFormatMismatch}>
          {#if audioFormatMismatch}
            <Icon name="alert" size={13} />
            <span title={audioFormatHint + '；无法解码，映射会产生噪声，请两端升级到同一版本'}>
              对端音频协议不可识别（疑似旧版），需两端同版本
            </span>
          {:else if !hasSystemAudio}
            <span>对端未开启系统声音共享</span>
          {:else if sysAudioMapped}
            <span class="status-dot" class:is-live={!audioSuspended} class:is-warn={audioSuspended} aria-hidden="true"></span>
            <span title={audioPlayState.text}>{audioSuspended ? '播放器被系统挂起' : '正在播放'}</span>
            <span class="meta-dot" aria-hidden="true"></span>
            <span class="mono" title={audioRateTitle}>{sysRateKBps} KB/s</span>
            {#if connectionText}
              <span class="meta-dot" aria-hidden="true"></span>
              <span>{connectionText}</span>
            {/if}
            {#if audioSuspended}
              <button class="link-btn" onclick={resumeAudio}>点击恢复播放</button>
            {/if}
          {:else}
            <span>把对端的扬声器输出映射到本机播放</span>
          {/if}
        </div>
      </div>
      <button
        class="cap-btn"
        class:is-on={sysAudioMapped}
        disabled={!hasSystemAudio || audioFormatMismatch}
        title={audioFormatMismatch ? audioFormatHint + '；无法解码，映射会产生噪声' : ''}
        onclick={onToggleSystemAudio}
      >
        {audioFormatMismatch ? '版本不一致' : sysAudioTitle}
      </button>
    </div>

    <!-- 麦克风输入 -->
    <div class="cap-row" class:is-on={micAudioMapped} class:on-purple={micAudioMapped} class:is-warn={audioFormatMismatch}>
      <div class="cap-icon"><Icon name="mic" size={19} /></div>
      <div class="cap-main">
        <div class="cap-title">
          <span>麦克风</span>
          {#if hasMicAudio && provides.mic_device}
            <span class="cap-device" title={provides.mic_device}>{provides.mic_device}</span>
          {/if}
        </div>
        <div class="cap-desc" class:is-warn={audioFormatMismatch}>
          {#if audioFormatMismatch}
            <Icon name="alert" size={13} />
            <span title={audioFormatHint + '；无法解码，映射会产生噪声，请两端升级到同一版本'}>
              对端音频协议不可识别（疑似旧版），需两端同版本
            </span>
          {:else if !hasMicAudio}
            <span>对端未开启麦克风共享</span>
          {:else if micAudioMapped}
            <span class="status-dot is-live" aria-hidden="true"></span>
            <span title={audioPlayState.text}>正在收听</span>
            <span class="meta-dot" aria-hidden="true"></span>
            <span class="mono" title={audioRateTitle}>{micRateKBps} KB/s</span>
          {:else}
            <span>把对端的麦克风拾音映射到本机播放</span>
          {/if}
        </div>
      </div>
      <button
        class="cap-btn"
        class:is-on={micAudioMapped}
        disabled={!hasMicAudio || audioFormatMismatch}
        title={audioFormatMismatch ? audioFormatHint + '；无法解码，映射会产生噪声' : ''}
        onclick={onToggleMicAudio}
      >
        {audioFormatMismatch ? '版本不一致' : micAudioTitle}
      </button>
    </div>

    <!-- 物理鼠标同步 -->
    <div class="cap-row" class:is-on={mouseMapped} class:on-blue={mouseMapped}>
      <div class="cap-icon"><Icon name="mouse" size={19} /></div>
      <div class="cap-main">
        <div class="cap-title"><span>物理鼠标</span></div>
        <div class="cap-desc">
          {#if !hasMouse}
            <span>对端未提供鼠标</span>
          {:else if mouseMapped}
            <span class="status-dot is-live" aria-hidden="true"></span>
            <span class="mono telemetry" title={cursorText || '等待对端鼠标事件'}>
              {cursorText || '等待对端鼠标事件…'}
            </span>
          {:else}
            <span>同步对端光标移动、点击与滚轮</span>
          {/if}
        </div>
      </div>
      <button class="cap-btn" class:is-on={mouseMapped} disabled={!hasMouse} onclick={onToggleMouse}>
        {mouseTitle}
      </button>
    </div>

    <!-- 物理键盘同步 -->
    <div class="cap-row" class:is-on={keyboardMapped} class:on-amber={keyboardMapped}>
      <div class="cap-icon"><Icon name="keyboard" size={19} /></div>
      <div class="cap-main">
        <div class="cap-title"><span>物理键盘</span></div>
        <div class="cap-desc">
          {#if !hasKeyboard}
            <span>对端未提供键盘</span>
          {:else if keyboardMapped}
            <span class="status-dot is-live" aria-hidden="true"></span>
            <span class="mono telemetry" title={keyboardText || '等待对端键盘事件'}>
              {keyboardText || '等待对端键盘事件…'}
            </span>
          {:else}
            <span>同步对端物理按键（含小键盘与修饰键）</span>
          {/if}
        </div>
      </div>
      <button class="cap-btn" class:is-on={keyboardMapped} disabled={!hasKeyboard} onclick={onToggleKeyboard}>
        {keyboardTitle}
      </button>
    </div>
  </div>
</article>
