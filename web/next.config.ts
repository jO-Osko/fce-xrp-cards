import path from "node:path";
import { fileURLToPath } from "node:url";
import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  reactStrictMode: true,
  // The operator pages moved under /global when the app split into a cardholder
  // and an operator side; keep old links working.
  async redirects() {
    return [
      { source: "/settle", destination: "/global/settlement", permanent: false },
      { source: "/broker", destination: "/global/broker", permanent: false },
      { source: "/liquidity", destination: "/lp/liquidity", permanent: false },
    ];
  },
  turbopack: {
    root: path.dirname(fileURLToPath(import.meta.url)),
  },
};

export default nextConfig;
