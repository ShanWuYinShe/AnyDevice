<script>
  // 通用弹窗外壳：
  //   - 按需挂载（关闭时不在 DOM 中），避免隐藏弹窗里的表单控件仍可被 Tab 聚焦；
  //   - 打开时锁背景滚动、焦点移到弹窗内，关闭后还给原焦点元素；
  //   - Tab/Shift+Tab 在弹窗内循环（焦点陷阱），不会跑到背景的映射开关上；
  //   - 遮罩点击与 Escape 均可关闭（遮罩本身是装饰容器，键盘路径由 Escape 提供）。
  import { tick } from 'svelte';
  import Icon from './Icon.svelte';

  let { open = false, title = '', onClose, children } = $props();

  let dialogEl = $state(null);
  let lastFocused = null;

  function focusableInDialog() {
    if (!dialogEl) return [];
    const nodes = dialogEl.querySelectorAll(
      'button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), a[href], [tabindex]:not([tabindex="-1"])'
    );
    return [...nodes].filter((el) => el.offsetParent !== null);
  }

  function handleKeydown(e) {
    if (!open) return;
    if (e.key === 'Escape') {
      e.preventDefault();
      onClose();
      return;
    }
    if (e.key !== 'Tab') return;
    const items = focusableInDialog();
    if (items.length === 0) return;
    const first = items[0];
    const last = items[items.length - 1];
    const active = document.activeElement;
    if (e.shiftKey && (active === first || !dialogEl?.contains(active))) {
      e.preventDefault();
      last.focus();
    } else if (!e.shiftKey && (active === last || !dialogEl?.contains(active))) {
      e.preventDefault();
      first.focus();
    }
  }

  $effect(() => {
    if (!open) return;
    lastFocused = document.activeElement;
    const prevOverflow = document.body.style.overflow;
    document.body.style.overflow = 'hidden';
    tick().then(() => {
      const target = dialogEl && (dialogEl.querySelector('[data-autofocus]') || dialogEl.querySelector('.modal-close'));
      if (target) target.focus();
    });
    return () => {
      document.body.style.overflow = prevOverflow;
      if (lastFocused && typeof lastFocused.focus === 'function') lastFocused.focus();
    };
  });
</script>

<svelte:window onkeydown={handleKeydown} />

{#if open}
  <div
    class="modal-overlay"
    role="presentation"
    onclick={(e) => { if (e.target === e.currentTarget) onClose(); }}
  >
    <div class="modal-card" role="dialog" aria-modal="true" aria-labelledby="modal-title" bind:this={dialogEl}>
      <div class="modal-header">
        <h2 class="modal-title" id="modal-title">{title}</h2>
        <button class="icon-btn modal-close" aria-label="关闭弹窗" onclick={onClose}>
          <Icon name="x" size={18} />
        </button>
      </div>
      {@render children?.()}
    </div>
  </div>
{/if}
