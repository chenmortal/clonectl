import react from "@vitejs/plugin-react";
import fs from "node:fs";
import path from "node:path";
import { defineConfig, type Plugin } from "vite";

// Dev proxy mirrors production same-origin: /api, /rclone, /healthz → API.
// Override with VITE_API_TARGET when the backend runs on a non-default port.
const apiTarget = process.env.VITE_API_TARGET ?? "http://localhost:8000";

// dist/web/.gitkeep 是 go:embed all:dist/web 的占位符——干净 checkout 没跑
// 过前端构建时靠它保证目录非空可编译。emptyOutDir 会把它清掉,构建收尾时
// 写回(append 模式只创建不截断),任何入口跑 build 都不会再丢。
function keepEmbedPlaceholder(): Plugin {
  return {
    name: "keep-embed-placeholder",
    closeBundle() {
      fs.closeSync(fs.openSync(path.resolve(__dirname, "../dist/web/.gitkeep"), "a"));
    },
  };
}

export default defineConfig({
  plugins: [react(), keepEmbedPlaceholder()],
  resolve: {
    alias: {
      "@": path.resolve(__dirname, "./src"),
    },
  },
  server: {
    port: 5173,
    proxy: {
      "/api": { target: apiTarget, changeOrigin: true },
      "/rclone": { target: apiTarget, changeOrigin: true },
      "/healthz": { target: apiTarget, changeOrigin: true },
    },
  },
  build: {
    outDir: "../dist/web",
    emptyOutDir: true,
    chunkSizeWarningLimit: 1200,
    // vite/rollup preserves the "src/" prefix under outDir; flatten.
    rollupOptions: {
      input: path.resolve(__dirname, "src/index.html"),
    },
  },
});
