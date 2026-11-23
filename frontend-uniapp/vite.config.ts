import { defineConfig } from "vite";
import uni from "@dcloudio/vite-plugin-uni";

export default defineConfig({
  base: "/uniapp/",
  plugins: [uni()],
  server: {
    port: 5174,
    strictPort: true,
    proxy: {
      "/api": process.env.FASTEMS_API || "http://127.0.0.1:62943",
    },
  },
  build: {
    outDir: "../internal/web/dist/uniapp",
    emptyOutDir: true,
  },
});
