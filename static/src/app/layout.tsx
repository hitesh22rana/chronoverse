import type { Metadata } from "next";
import { Poppins as FontPoppins } from 'next/font/google'

import { ThemeProvider } from "@/components/theme-provider"

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

export const metadata:Metadata = {
	title: 'Chronoverse - Distributed Task Scheduler & Orchestrator',
	description:
		'Chronoverse is a distributed, job scheduling and orchestration system designed for reliability and scalability.',
	keywords: [
		'chronoverse',
		'task scheduler',
		'job orchestrator',
		'distributed system',
		'microservices',
		'golang',
	],
}

export default function RootLayout({
  children,
}: Readonly<{
  children: React.ReactNode;
}>) {
  return (
    <html
      lang="en"
      suppressHydrationWarning
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
          {children}
        </ThemeProvider>
      </body>
    </html>
  );
}
