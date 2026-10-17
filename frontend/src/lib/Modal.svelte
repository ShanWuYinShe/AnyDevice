<script>
  // 通用弹窗外壳：遮罩点击与关闭按钮回调 onClose；Escape 键同样可关闭
  let { open = false, title = '', onClose, children } = $props();

  function handleKeydown(e) {
    if (open && e.key === 'Escape') onClose();
  }
</script>

<svelte:window onkeydown={handleKeydown} />

<!-- 遮罩是纯装饰容器：点击关闭由关闭按钮与 Escape 键提供等效键盘可达路径 -->
<div class="modal-overlay" role="presentation" class:open={open} onclick={(e) => { if (e.target === e.currentTarget) onClose(); }}>
  <div class="modal-card">
    <div class="modal-header">
      <h2>{title}</h2>
      <button class="close-modal-btn" onclick={onClose}>✕</button>
    </div>
    {@render children?.()}
  </div>
</div>
