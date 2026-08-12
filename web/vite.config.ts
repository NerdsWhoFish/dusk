import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// Asset names keep Vite's content hash. `go:embed all:dist` takes the whole
// tree whatever it is called, and hashless names served as immutable would
// pin every browser that ever loaded the app to the first build it saw.
export default defineConfig({
  plugins: [react()],
  build: {
    outDir: "dist",
    emptyOutDir: true,
  },
  server: {
    // `npm run dev` talks to a locally running Dusk, so the UI can be worked
    // on with hot reload against a real catalog.
    proxy: {
      "/api": "http://localhost:8080",
      "/login": "http://localhost:8080",
      "/logout": "http://localhost:8080",
    },
  },
});
