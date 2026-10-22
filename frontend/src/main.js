import { mount } from 'svelte';
import './app.css';
import App from './App.svelte';

// 桌面原生化：屏蔽默认浏览器上下文菜单，保留输入框的原生编辑能力
window.addEventListener('contextmenu', (e) => {
  if (e.target instanceof HTMLInputElement || e.target instanceof HTMLTextAreaElement) {
    return;
  }
  e.preventDefault();
});

const app = mount(App, { target: document.getElementById('app') });

export default app;
