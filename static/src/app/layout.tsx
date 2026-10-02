import type { Metadata } from "next";

import { SearchProvider } from "@/components/docs/search-dialog";
import { SiteFooter } from "@/components/site-footer";
import { SiteHeader } from "@/components/site-header";
import { ThemeProvider } from "@/components/theme-provider";
import { SITE_URL, SOCIAL_IMAGE_ALT, SOCIAL_IMAGE_TYPE, SOCIAL_IMAGE_URL, siteAssetUrl, sitePageUrl } from "@/lib/site";

import "./globals.css";

// GitHub Pages serves static files and cannot send custom response headers, so the
// policy ships as a meta tag, and only in production: React's dev build needs
// eval() for callstacks and Turbopack HMR needs a websocket, neither of which
// this policy allows. It still blocks third-party script, object and base
// injection. frame-ancestors only takes effect once the site is served with real
// headers.
const CONTENT_SECURITY_POLICY = [
  "default-src 'self'",
  "base-uri 'self'",
  "object-src 'none'",
  "frame-ancestors 'none'",
  "form-action 'self'",
  "script-src 'self' 'unsafe-inline'",
  "style-src 'self' 'unsafe-inline'",
  "img-src 'self' data:",
  "font-src 'self'",
  "connect-src 'self'",
  "upgrade-insecure-requests",
].join("; ");

export const metadata: Metadata = {
  metadataBase: new URL(SITE_URL),
  title: { default: "Chronoverse · Distributed scheduler and orchestrator", template: "%s · Chronoverse" },
  description: "A self-hosted distributed scheduler for heartbeat checks and container workloads with replay-safe execution, searchable logs, and full observability.",
  alternates: { canonical: sitePageUrl() },
  openGraph: {
    title: "Chronoverse",
    description: "Reliable scheduled work, on infrastructure you control.",
    type: "website",
    url: sitePageUrl(),
    siteName: "Chronoverse",
    locale: "en_US",
    images: [{ url: SOCIAL_IMAGE_URL, type: SOCIAL_IMAGE_TYPE, width: 1200, height: 630, alt: SOCIAL_IMAGE_ALT }],
  },
  twitter: {
    card: "summary_large_image",
    title: "Chronoverse",
    description: "Reliable scheduled work, on infrastructure you control.",
    images: [{ url: SOCIAL_IMAGE_URL, alt: SOCIAL_IMAGE_ALT }],
  },
};

export default function RootLayout({ children }: Readonly<{ children: React.ReactNode }>) {
  return (
    <html data-scroll-behavior="smooth" lang="en" suppressHydrationWarning>
      <head>
        {process.env.NODE_ENV === "production" && <meta httpEquiv="Content-Security-Policy" content={CONTENT_SECURITY_POLICY} />}
        <link rel="alternate" type="text/plain" href={siteAssetUrl("/llms.txt")} title="llms.txt" />
      </head>
      <body>
        <ThemeProvider attribute="class" defaultTheme="dark" enableSystem disableTransitionOnChange>
          <SearchProvider>
            <a className="skip-link" href="#main-content">Skip to content</a>
            <SiteHeader />
            <div id="main-content">{children}</div>
            <SiteFooter />
          </SearchProvider>
        </ThemeProvider>
      </body>
    </html>
  );
}
