import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  reactCompiler: true,
  experimental: {
    viewTransition: true,
  },
  typescript: {
    ignoreBuildErrors: true,
  },
  output: 'standalone',
};

export default nextConfig;
