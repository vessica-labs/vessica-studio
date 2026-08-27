import { defineConfig } from "astro/config";
import mdx from "@astrojs/mdx";

export default defineConfig({
  site: "https://studio-docs.vessica.ai",
  integrations: [mdx()],
  trailingSlash: "always",
  build: { format: "directory" },
});
