<script>
  import Icon from './Icon.svelte';
  import {
    telemetry, audioRate, audioPlayState, audioStats,
    openRenamePeer, toggleMapping, primeAudio, resumeAudio, formatOs,
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
  const osLabel = $derived(formatOs(peer.os));
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

  // —— 播放链路延迟估算（分侧定位：对端采集/编码/发送 · 网络 · 本机缓冲/输出） ——
  // 数据来源：mapping.net_rtt_ms 与 mapping.sender_{sys,mic}_latency（后端新增字段，
  // 旧版对端缺失=undefined，必须容错——缺字段时只显示本机侧+网络分项）；
  // audioStats（本机 AudioWorklet 水位 + AudioContext 输出延迟，见 state.svelte.js）。
  // 注意：各段耗时相加只是估算，不是端到端时钟同步测量（title 里注明）。
  function numOr0(v) {
    const n = Number(v);
    return Number.isFinite(n) ? n : 0;
  }

  function buildLatency(stream) {
    const sender = stream === 'mic' ? mapping.sender_mic_latency : mapping.sender_sys_latency;
    const stats = audioStats[stream] || {};
    // running 为 false 时该流采集未运行，采集端三项不计入
    const senderRunning = !!(sender && sender.running);
    const senderMs = senderRunning
      ? numOr0(sender.chunk_ms) + numOr0(sender.encode_ms) + numOr0(sender.send_ms)
      : 0;
    const netRtt = numOr0(mapping.net_rtt_ms);
    const netMs = netRtt / 2; // RTT 单程 = /2
    const bufferMs = numOr0(stats.bufferMs);
    const targetMs = numOr0(stats.targetMs); // 自适应目标水位（0=尚未收到 worklet 统计）
    // 对端编码参数（发送端自适应新增：bitrate=比特率 bps / complexity / level 档位）。
    // 旧版对端不报这三项：bitrate/complexity 带 omitempty 而缺失，但 level 不带
    // omitempty 会退化成 0（与合法档位「省 CPU」同值），无法区分。因此只有出现
    // 新版 bitrate/complexity 字段时才认为编码信息可信，避免把旧对端误标成低档位。
    const bitrateKbps = sender ? Math.round(numOr0(sender.bitrate) / 1000) : 0;
    const complexity = sender && sender.complexity != null ? Math.round(numOr0(sender.complexity)) : -1;
    const level = sender && sender.level != null ? Math.round(numOr0(sender.level)) : -1;
    // 旧对端电平门控：只认 bitrate 是否存在（后端契约：均衡档必报 bitrate=128000）。
    // 旧对端 bitrate/complexity 缺失、level 因无 omitempty 退化成 0，仅凭 level 无法区分。
    const encodingKnown = bitrateKbps > 0;
    const outputMs = numOr0(audioStats.outputMs);
    return {
      sender, // null/undefined = 旧版对端未上报
      senderRunning,
      netKnown: netRtt > 0, // 契约：0/缺失 = 网络延迟未知
      chunkMs: numOr0(sender && sender.chunk_ms),
      encodeMs: numOr0(sender && sender.encode_ms),
      sendMs: numOr0(sender && sender.send_ms),
      senderMs,
      netMs,
      bufferMs,
      targetMs,
      bitrateKbps, // 0=未上报
      complexity, // -1=未上报
      level, // -1=未上报
      encodingKnown,
      outputMs,
      underflows: numOr0(stats.underflows),
      totalMs: Math.round(senderMs + netMs + bufferMs + outputMs),
    };
  }

  const sysLatency = $derived(buildLatency('sys'));
  const micLatency = $derived(buildLatency('mic'));

  // 发送端自适应档位文案（后端契约：0=省 CPU / 1=平衡 / 2=音质优先）
  function adaptiveLevelText(level) {
    if (level === 0) return '省 CPU（高负载保护）';
    if (level === 1) return '平衡';
    if (level === 2) return '音质优先';
    return '未知(' + level + ')';
  }

  // 延迟 chip 的多行 title：分项明细 + 计算口径 + 欠载含义
  function latencyTitle(label, l) {
    const parts = [];
    if (l.senderRunning) {
      parts.push(
        '采集块 ' + Math.round(l.chunkMs) + 'ms',
        '编码 ' + Math.round(l.encodeMs) + 'ms',
        '发送 ' + Math.round(l.sendMs) + 'ms'
      );
    }
    parts.push(
      l.netKnown ? '网络 ' + Math.round(l.netMs) + 'ms' : '网络 未知',
      // 实际水位 + 自适应目标：一眼看出是「低延迟稳定运行」还是「被欠载抬高目标」
      '播放缓冲 ' + Math.round(l.bufferMs) + 'ms'
        + (l.targetMs > 0 ? '（自适应目标 ' + Math.round(l.targetMs) + 'ms）' : ''),
      '输出 ' + Math.round(l.outputMs) + 'ms'
    );
    const lines = [
      '估算总延迟 ~' + l.totalMs + 'ms（' + label + '）',
      parts.join(' + '),
    ];
    if (!l.sender) {
      lines.push('对端未上报采集端延迟（旧版对端），仅计网络与本机播放侧');
    } else if (!l.senderRunning) {
      lines.push('对端该流当前未采集（running=false），采集端延迟不计入');
    }
    if (!l.netKnown) {
      lines.push('网络 RTT 未知（旧服务端或尚未测速），网络延迟按 0 计');
    }
    // 对端编码参数（发送端自适应档位）；旧版对端无这些字段时整行不显示
    if (l.encodingKnown) {
      const enc = [];
      if (l.bitrateKbps > 0) enc.push('对端编码 ' + l.bitrateKbps + 'kbps');
      if (l.complexity >= 0) enc.push('复杂度 ' + l.complexity);
      if (l.level >= 0) enc.push('自适应档 ' + adaptiveLevelText(l.level));
      if (enc.length > 0) lines.push(enc.join(' · '));
    }
    lines.push('播放欠载 ' + l.underflows + ' 次（本机负载高时会重新预缓冲；数字持续增长说明接收端吃力）');
    lines.push('估算值，非端到端时钟同步测量；输出延迟部分环境不可测时按 0 计');
    return lines.join('\n');
  }

  // 欠载计数的 title：说明「持续增长 = 接收端负载高、在反复重新预缓冲」
  function underflowTitle(l) {
    return '播放欠载 ' + l.underflows + ' 次：环形缓冲被抽空后重新预缓冲（会短暂静音）。'
      + '数字持续增长说明接收端（本机）负载高、在反复重新预缓冲，与对端采集无关。';
  }

  const cursorText = $derived(String(telemetry.cursor[peer.id] || '').replace(/^●\s*/, ''));
  const keyboardText = $derived(String(telemetry.keyboard[peer.id] || '').replace(/^●\s*/, ''));

  const sysAudioTitle = $derived(sysAudioMapped ? '停止播放' : '映射播放');
  const micAudioTitle = $derived(micAudioMapped ? '停止收听' : '映射收听');
  const mouseTitle = $derived(mouseMapped ? '停止同步' : '映射同步');
  const keyboardTitle = $derived(keyboardMapped ? '停止同步' : '映射同步');

  function onToggleSystemAudio() {
    const next = !sysAudioMapped;
    if (next) primeAudio(); // 仅开启时预热 AudioContext，关闭无需创建
    // 开启时带上设备名：若对端是占位测试音（未装虚拟声卡）用户能立即识别
    const dev = provides.audio_device ? '（' + provides.audio_device + '）' : '';
    toggleMapping(peer, 'play_system_audio', next,
      '已开启播放对端系统声音 ' + dev, '已停止播放系统声音');
  }
  function onToggleMicAudio() {
    const next = !micAudioMapped;
    if (next) primeAudio();
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
            <span class="meta-dot" aria-hidden="true"></span>
            <span class="mono latency-chip" title={latencyTitle('系统声音', sysLatency)}>≈{sysLatency.totalMs}ms</span>
            {#if sysLatency.underflows > 0}
              <span class="latency-warn" title={underflowTitle(sysLatency)}>欠载 {sysLatency.underflows}</span>
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
            <span class="meta-dot" aria-hidden="true"></span>
            <span class="mono latency-chip" title={latencyTitle('麦克风', micLatency)}>≈{micLatency.totalMs}ms</span>
            {#if micLatency.underflows > 0}
              <span class="latency-warn" title={underflowTitle(micLatency)}>欠载 {micLatency.underflows}</span>
            {/if}
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
