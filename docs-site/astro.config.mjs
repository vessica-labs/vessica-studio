import { defineConfig } from "astro/config";
import mdx from "@astrojs/mdx";

export default defineConfig({
  site: "https://vessica-studio-docs-production.up.railway.app",
  integrations: [mdx()],
  trailingSlash: "always",
  build: { format: "directory" },
});
