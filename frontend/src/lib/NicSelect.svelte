<script>
  import { onMount, onDestroy } from 'svelte';
  import Icon from './Icon.svelte';

  /**
   * NicSelect.svelte: 现代化网卡通道下拉选择器
   * 代替原始丑陋的系统原生 select，具备双行信息（网卡名 + IP）、虚拟网卡标签、选中状态与平滑动画。
   */
  let {
    interfaces = [],
    value = '',
    onchange = () => {},
  } = $props();

  let open = $state(false);
  let containerRef = $state(null);

  const current = $derived(
    interfaces.find((item) => item.ip === value) ||
      (value ? { name: '通道', ip: value, is_virtual: false } : null)
  );

  function toggleOpen(e) {
    e.stopPropagation();
    open = !open;
  }

  function selectOption(ip, e) {
    e?.stopPropagation();
    if (ip !== value) {
      onchange(ip);
    }
    open = false;
  }

  function handleKeydown(e) {
    if (e.key === 'Escape' && open) {
      open = false;
    }
  }

  function handleOutsideClick(e) {
    if (open && containerRef && !containerRef.contains(e.target)) {
      open = false;
    }
  }

  onMount(() => {
    window.addEventListener('click', handleOutsideClick);
    window.addEventListener('keydown', handleKeydown);
  });

  onDestroy(() => {
    window.removeEventListener('click', handleOutsideClick);
    window.removeEventListener('keydown', handleKeydown);
  });
</script>

<div class="nic-select-wrapper" bind:this={containerRef}>
  <button
    type="button"
    class="nic-trigger"
    class:is-open={open}
    onclick={toggleOpen}
    aria-haspopup="listbox"
    aria-expanded={open}
    title="切换对外广播与服务的网络通道"
  >
    {#if current}
      <div class="nic-selected-info">
        <div class="nic-selected-title">
          <span class="nic-name">{current.name}</span>
          {#if current.is_virtual}
            <span class="nic-badge-virtual">虚拟</span>
          {/if}
        </div>
        <span class="nic-ip mono">{current.ip}</span>
      </div>
    {:else}
      <div class="nic-selected-info">
        <span class="nic-placeholder">载入网卡中…</span>
      </div>
    {/if}

    <div class="nic-arrow" class:is-rotated={open}>
      <Icon name="chevronDown" size={15} />
    </div>
  </button>

  {#if open}
    <div class="nic-dropdown" role="listbox">
      <div class="nic-dropdown-header">选择网络通道</div>
      <div class="nic-options-list">
        {#if interfaces && interfaces.length > 0}
          {#each interfaces as iface (iface.name + '|' + iface.ip)}
            {@const isSelected = iface.ip === value}
            <button
              type="button"
              class="nic-option"
              class:is-selected={isSelected}
              role="option"
              aria-selected={isSelected}
              onclick={(e) => selectOption(iface.ip, e)}
            >
              <div class="nic-option-body">
                <div class="nic-option-title-row">
                  <span class="nic-option-name">{iface.name}</span>
                  {#if iface.is_virtual}
                    <span class="nic-badge-virtual">虚拟网卡</span>
                  {/if}
                </div>
                <div class="nic-option-ip mono">{iface.ip}</div>
              </div>
              <div class="nic-option-indicator">
                {#if isSelected}
                  <span class="nic-check-icon">
                    <Icon name="check" size={15} />
                  </span>
                {/if}
              </div>
            </button>
          {/each}
        {:else}
          <div class="nic-empty-text">未检测到可用网卡</div>
        {/if}
      </div>
    </div>
  {/if}
</div>

<style>
  .nic-select-wrapper {
    position: relative;
    width: 100%;
    max-width: 260px;
    user-select: none;
  }

  .nic-trigger {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 10px;
    width: 100%;
    min-height: 40px;
    padding: 6px 12px;
    background: rgba(255, 255, 255, 0.045);
    border: 1px solid rgba(255, 255, 255, 0.1);
    border-radius: var(--radius-md);
    color: var(--text);
    cursor: pointer;
    font: inherit;
    text-align: left;
    transition: all 0.15s cubic-bezier(0.2, 0.7, 0.3, 1);
    box-shadow: 0 1px 3px rgba(0, 0, 0, 0.35), inset 0 1px 0 rgba(255, 255, 255, 0.06);
  }

  .nic-trigger:hover {
    background: rgba(255, 255, 255, 0.08);
    border-color: rgba(255, 255, 255, 0.18);
  }

  .nic-trigger:active {
    transform: scale(0.99);
  }

  .nic-trigger.is-open {
    border-color: var(--blue);
    background: rgba(255, 255, 255, 0.08);
    box-shadow: 0 0 0 3px rgba(91, 157, 255, 0.25), inset 0 1px 0 rgba(255, 255, 255, 0.08);
  }

  .nic-selected-info {
    display: flex;
    flex-direction: column;
    gap: 1px;
    min-width: 0;
    flex: 1;
  }

  .nic-selected-title {
    display: flex;
    align-items: center;
    gap: 6px;
    min-width: 0;
  }

  .nic-name {
    font-size: 0.84rem;
    font-weight: 650;
    color: var(--text);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .nic-ip {
    font-size: 0.73rem;
    color: var(--text-2);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .nic-placeholder {
    font-size: 0.82rem;
    color: var(--text-3);
  }

  .nic-badge-virtual {
    display: inline-flex;
    align-items: center;
    padding: 0 5px;
    height: 16px;
    border-radius: 4px;
    font-size: 0.62rem;
    font-weight: 500;
    background: rgba(167, 139, 250, 0.15);
    border: 1px solid rgba(167, 139, 250, 0.3);
    color: #c4b5fd;
    white-space: nowrap;
  }

  .nic-arrow {
    display: grid;
    place-items: center;
    flex: none;
    color: var(--text-3);
    transition: transform 0.22s cubic-bezier(0.2, 0.7, 0.3, 1), color 0.2s;
  }

  .nic-trigger:hover .nic-arrow {
    color: var(--text-2);
  }

  .nic-arrow.is-rotated {
    transform: rotate(180deg);
    color: var(--blue);
  }

  /* 下拉面板：原生 macOS NSMenu 风格高质感浮层 */
  .nic-dropdown {
    position: absolute;
    top: calc(100% + 5px);
    left: 0;
    right: 0;
    min-width: 250px;
    z-index: 99;
    padding: 5px;
    background: #0d1525;
    border: 1px solid rgba(255, 255, 255, 0.14);
    border-radius: 11px;
    box-shadow: 0 18px 42px -6px rgba(0, 0, 0, 0.9),
      0 0 0 1px rgba(0, 0, 0, 0.6);
    animation: dropdown-in 0.14s cubic-bezier(0.2, 0.7, 0.3, 1);
  }

  @keyframes dropdown-in {
    from {
      opacity: 0;
      transform: translateY(-6px) scale(0.975);
    }
    to {
      opacity: 1;
      transform: translateY(0) scale(1);
    }
  }

  .nic-dropdown-header {
    padding: 6px 10px 6px;
    font-size: 0.66rem;
    font-weight: 600;
    letter-spacing: 0.05em;
    color: var(--text-3);
    border-bottom: 1px solid rgba(255, 255, 255, 0.06);
    margin-bottom: 4px;
    text-transform: uppercase;
  }

  .nic-options-list {
    display: flex;
    flex-direction: column;
    gap: 2px;
    max-height: 240px;
    overflow-y: auto;
    scrollbar-width: thin;
    overscroll-behavior: contain;
  }

  .nic-option {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 10px;
    width: 100%;
    padding: 7px 10px;
    border: 1px solid transparent;
    border-radius: 7px;
    background: transparent;
    color: var(--text);
    cursor: pointer;
    text-align: left;
    font: inherit;
    transition: all 0.12s ease;
  }

  .nic-option:hover {
    background: rgba(59, 130, 246, 0.18);
    border-color: rgba(59, 130, 246, 0.3);
  }

  .nic-option.is-selected {
    background: rgba(91, 157, 255, 0.14);
    border-color: rgba(91, 157, 255, 0.32);
  }

  .nic-option-body {
    display: flex;
    flex-direction: column;
    gap: 2px;
    min-width: 0;
    flex: 1;
  }

  .nic-option-title-row {
    display: flex;
    align-items: center;
    gap: 6px;
  }

  .nic-option-name {
    font-size: 0.83rem;
    font-weight: 600;
    color: var(--text);
  }

  .nic-option.is-selected .nic-option-name {
    color: #9dc2ff;
  }

  .nic-option-ip {
    font-size: 0.74rem;
    color: var(--text-2);
  }

  .nic-option.is-selected .nic-option-ip {
    color: #bfdbfe;
  }

  .nic-option-indicator {
    display: grid;
    place-items: center;
    width: 18px;
    height: 18px;
    flex: none;
  }

  .nic-check-icon {
    color: var(--blue);
    display: flex;
  }

  .nic-empty-text {
    padding: 12px;
    text-align: center;
    font-size: 0.78rem;
    color: var(--text-3);
  }

  @media (max-width: 900px) {
    .nic-select-wrapper {
      max-width: 100%;
    }
  }
</style>
