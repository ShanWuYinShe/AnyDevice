<script>
  // 轻量内联图标集：统一 24x24 viewBox、描边风格，随 currentColor 变色。
  // 使用矢量图标而非 emoji：emoji 在不同平台/字体下形态不一致、无法随状态变色，
  // 对导航与操作按钮会显得杂乱（Windows/Mac/iOS 渲染差异明显）。
  const PATHS = {
    sync: '<path d="M21 12a9 9 0 0 1-15.5 6.2L3 16"/><path d="M3 21v-5h5"/><path d="M3 12a9 9 0 0 1 15.5-6.2L21 8"/><path d="M21 3v5h-5"/>',
    qr: '<rect x="3" y="3" width="7" height="7" rx="1.5"/><rect x="14" y="3" width="7" height="7" rx="1.5"/><rect x="3" y="14" width="7" height="7" rx="1.5"/><path d="M14 14h3v3h-3z"/><path d="M20.5 14v3.5"/><path d="M14 20.5h3"/><path d="M20.5 20.5h.01"/>',
    plus: '<path d="M12 5v14"/><path d="M5 12h14"/>',
    sliders: '<path d="M4 7h9"/><path d="M17 7h3"/><path d="M4 17h3"/><path d="M11 17h9"/><circle cx="15" cy="7" r="2"/><circle cx="9" cy="17" r="2"/>',
    refresh: '<path d="M21 12a9 9 0 1 1-9-9c2.52 0 4.93 1 6.74 2.74L21 8"/><path d="M21 3v5h-5"/>',
    pencil: '<path d="M4 20h4L19 9a2.83 2.83 0 0 0-4-4L4 16v4Z"/><path d="m13.5 6.5 4 4"/>',
    monitor: '<rect x="3" y="4.5" width="18" height="12" rx="2"/><path d="M9 20h6"/><path d="M12 16.5V20"/>',
    laptop: '<rect x="4.5" y="5.5" width="15" height="10" rx="1.6"/><path d="M2.5 19.5h19"/>',
    phone: '<rect x="7" y="2.8" width="10" height="18.4" rx="2.4"/><path d="M11 18.4h2"/>',
    speaker: '<path d="M11 5 6.5 9H3.5v6h3L11 19V5Z"/><path d="M15 9.2a4 4 0 0 1 0 5.6"/><path d="M18 6.6a8 8 0 0 1 0 10.8"/>',
    mic: '<rect x="9" y="3" width="6" height="11" rx="3"/><path d="M5.5 11.5a6.5 6.5 0 0 0 13 0"/><path d="M12 18v3"/>',
    mouse: '<rect x="6" y="2.8" width="12" height="18.4" rx="6"/><path d="M12 6.5v3.5"/>',
    keyboard: '<rect x="2.5" y="6" width="19" height="12" rx="2"/><path d="M6 10.5h12"/><path d="M6 14.5h8"/>',
    wifi: '<path d="M2.5 8.5a15 15 0 0 1 19 0"/><path d="M5.5 12a10.5 10.5 0 0 1 13 0"/><path d="M8.6 15.4a6 6 0 0 1 6.8 0"/><path d="M12 19h.01"/>',
    link: '<path d="M10.2 13.8a3 3 0 0 0 4.2 0l3.2-3.2a3 3 0 0 0-4.2-4.2L11.6 8.2"/><path d="M13.8 10.2a3 3 0 0 0-4.2 0l-3.2 3.2a3 3 0 0 0 4.2 4.2l1.8-1.8"/>',
    alert: '<path d="M12 3.5 2.6 20h18.8L12 3.5Z"/><path d="M12 9.5v5"/><path d="M12 17.4h.01"/>',
    check: '<path d="M4.5 12.5l4.5 4.5L19.5 6.5"/>',
    checkCircle: '<circle cx="12" cy="12" r="9"/><path d="m8 12.2 2.6 2.6 5.4-5.6"/>',
    x: '<path d="M6 6l12 12"/><path d="M18 6 6 18"/>',
    copy: '<rect x="9" y="9" width="12" height="12" rx="2"/><path d="M5.5 15H4a1 1 0 0 1-1-1V4a1 1 0 0 1 1-1h10a1 1 0 0 1 1 1v1.5"/>',
  };

  let { name = 'link', size = 18, stroke = 1.8 } = $props();
  const inner = $derived(PATHS[name] || PATHS.link);
</script>

<svg
  width={size}
  height={size}
  viewBox="0 0 24 24"
  fill="none"
  stroke="currentColor"
  stroke-width={stroke}
  stroke-linecap="round"
  stroke-linejoin="round"
  aria-hidden="true"
  focusable="false"
>
  {@html inner}
</svg>
