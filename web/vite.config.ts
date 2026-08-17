import { defineConfig } from "vite";
import { resolve } from "node:path";

export default defineConfig({
  base: "./",
  build: {
    outDir: resolve(process.cwd(), "../internal/server/ui/dist"),
    emptyOutDir: true,
    assetsDir: "assets",
  },
});
