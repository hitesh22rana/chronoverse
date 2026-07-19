import type { Metadata } from "next";

import { SearchProvider } from "@/components/docs/search-dialog";
import { SiteFooter } from "@/components/site-footer";
import { SiteHeader } from "@/components/site-header";
import { ThemeProvider } from "@/components/theme-provider";
import { SITE_URL, SOCIAL_IMAGE_ALT, SOCIAL_IMAGE_TYPE, SOCIAL_IMAGE_URL, sitePageUrl } from "@/lib/site";

import "./globals.css";

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
