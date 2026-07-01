import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

export default defineConfig({
  base: "/console/",
  plugins: [react()],
  server: {
    port: 5173,
    strictPort: false,
    proxy: {
      "/internal": "http://127.0.0.1:18080",
    },
  },
});
