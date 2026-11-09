import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  reactStrictMode: true,
  output: "standalone",
  ...(process.env.E2E_INSECURE_ORIGIN
    ? { allowedDevOrigins: [new URL(process.env.E2E_INSECURE_ORIGIN).hostname] }
    : {}),
};

export default nextConfig;
