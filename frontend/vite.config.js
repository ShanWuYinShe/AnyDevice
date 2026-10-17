import { defineConfig } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';

// 构建产物直接输出到 Go 的 go:embed 目录（internal/web/dist），
// Go 编译时静态打包，无需任何运行时前端服务。
// 本地开发：bun run dev 启动 Vite（默认 5173），/api 与 /ws 代理到 Go 服务（默认 8080）。
export default defineConfig({
  plugins: [svelte()],
  build: {
    outDir: '../internal/web/dist',
    emptyOutDir: true,
  },
  server: {
    proxy: {
      '/api': 'http://127.0.0.1:8080',
      '/ws': { target: 'ws://127.0.0.1:8080', ws: true },
    },
  },
});
