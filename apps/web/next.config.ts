import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  reactStrictMode: true,
  output: "standalone",
  experimental: {
    agentUpgrade: "security",
  },
  ...(process.env.E2E_INSECURE_ORIGIN
    ? { allowedDevOrigins: [new URL(process.env.E2E_INSECURE_ORIGIN).hostname] }
    : {}),
};

export default nextConfig;
