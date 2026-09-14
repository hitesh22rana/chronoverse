import type { NextConfig } from "next";

// HSTS is ignored over plain HTTP (inert locally, active in prod).
// connect-src stays scheme-wide: the API origin is NEXT_PUBLIC_API_URL.
// script-src needs 'unsafe-inline': Next inlines bootstrap + __next_f flight
// data; strict nonces would need per-request middleware plumbing. Dev
// runtimes additionally eval for Fast Refresh and server-component stacks,
// so allow it outside production only.
const scriptSrc = ["'self'", "'unsafe-inline'"]
if (process.env.NODE_ENV !== "production") {
    scriptSrc.push("'unsafe-eval'")
}
const securityHeaders = [
    {
        key: "Strict-Transport-Security",
        value: "max-age=63072000; includeSubDomains",
    },
    {
        key: "X-Content-Type-Options",
        value: "nosniff",
    },
    {
        key: "X-Frame-Options",
        value: "DENY",
    },
    {
        key: "Referrer-Policy",
        value: "strict-origin-when-cross-origin",
    },
    {
        key: "Content-Security-Policy",
        value: [
            "default-src 'self'",
            `script-src ${scriptSrc.join(" ")}`,
            "style-src 'self' 'unsafe-inline'",
            "img-src 'self' data: https:",
            "font-src 'self' data:",
            "connect-src 'self' http: https:",
            "frame-ancestors 'none'",
            "base-uri 'self'",
            "form-action 'self'",
        ].join("; "),
    },
];

const nextConfig: NextConfig = {
  reactCompiler: true,
  experimental: {
    viewTransition: true,
  },
  typescript: {
    ignoreBuildErrors: true,
  },
  output: 'standalone',
  async headers() {
    return [
      {
        source: "/:path*",
        headers: securityHeaders,
      },
    ];
  },
};

export default nextConfig;
