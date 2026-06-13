import type { Metadata } from "next";

import { SearchProvider } from "@/components/docs/search-dialog";
import { SiteFooter } from "@/components/site-footer";
import { SiteHeader } from "@/components/site-header";
import { ThemeProvider } from "@/components/theme-provider";
import { SITE_URL } from "@/lib/site";

import "./globals.css";

export const metadata: Metadata = {
  metadataBase: new URL(SITE_URL),
  title: { default: "Chronoverse · Distributed scheduler and orchestrator", template: "%s · Chronoverse" },
  description: "A self-hosted distributed scheduler for heartbeat checks and container workloads with replay-safe execution, searchable logs, and full observability.",
  keywords: ["distributed scheduler", "workflow orchestration", "Go", "Kafka", "Docker", "job scheduler", "self-hosted"],
  openGraph: { title: "Chronoverse", description: "Reliable scheduled work, on infrastructure you control.", type: "website", url: SITE_URL },
};

export default function RootLayout({ children }: Readonly<{ children: React.ReactNode }>) {
  return (
    <html lang="en" suppressHydrationWarning>
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
