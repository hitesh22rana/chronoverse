import type { Metadata } from "next";
import { Poppins as FontPoppins } from 'next/font/google'
import { Toaster } from "sonner";

import { Providers } from "@/app/providers";
import { ThemeProvider } from "@/components/theme-provider";

import { cn } from "@/lib/utils";

import "./globals.css";

const fontSans = FontPoppins({
  weight: '400',
  subsets: ['latin'],
  variable: '--font-sans',
})

const fontHeading = FontPoppins({
  weight: '400',
  subsets: ['latin'],
  variable: '--font-heading',
})

export const metadata: Metadata = {
  title: "Chronoverse - Distributed Task Scheduler & Orchestrator",
  description: "Dashboard for Chronoverse, a distributed task scheduler and orchestrator."
};

export default function RootLayout({
  children,
}: Readonly<{
  children: React.ReactNode;
}>) {
  return (
    <html
      lang="en"
      suppressHydrationWarning
      data-lt-installed
      className="hydrated"
      data-scroll-behavior="smooth"
    >
      <body
        className={cn(
          'min-h-screen bg-background font-sans antialiased',
          fontSans.variable,
          fontHeading.variable,
        )}
      >
        <ThemeProvider
          attribute="class"
          defaultTheme="system"
          enableSystem
          disableTransitionOnChange
        >
          <Providers>
            {children}
            <Toaster position="top-right" />
          </Providers>
        </ThemeProvider>
      </body>
    </html>
  );
}
