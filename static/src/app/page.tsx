import Link from 'next/link'
import { Fragment } from 'react'

import {
  ArrowRight,
  Clock,
  Container,
  Globe,
  Lock,
  Server,
  Shield,
  CheckCircle2,
  FileCode,
} from 'lucide-react'

import { Button } from '@/components/ui/button'
import { BackgroundBeams } from '@/components/ui/background-beams'

export default function Home() {
  return (
    <Fragment>
      <BackgroundBeams className="fixed inset-0 -z-10 overflow-hidden" />

      <header className="px-4 py-2 w-full">
        <div className="mx-auto container flex h-16 items-center space-x-4 sm:justify-between sm:space-x-0">
          <div className="flex gap-6 md:gap-10 z-50">
            <Link href="#" className="flex items-center space-x-2 relative z-50">
              <span className="inline-block text-xl font-extrabold">Chronoverse</span>
            </Link>
          </div>
          <div className="flex flex-1 items-center justify-end space-x-4">
            <Link
              href="https://github.com/hitesh22rana/chronoverse"
              target="_blank"
              rel="noopener noreferrer"
              className="flex items-center gap-2 z-50"
            >
              <GithubIcon />
            </Link>
          </div>
        </div>
      </header>

      <main className="px-4 py-2 flex flex-col items-center justify-center w-full">
        <section id="hero" className="space-y-6 pb-8 pt-6 md:pb-12 md:pt-10 lg:py-32">
          <div className="container flex max-w-[64rem] flex-col items-center gap-4 text-center">
            <h1 className="font-heading text-3xl sm:text-5xl md:text-6xl lg:text-7xl">
              Chronoverse
            </h1>
            <p className="max-w-[42rem] leading-normal text-muted-foreground sm:text-xl sm:leading-8">
              Distributed Task Scheduler & Orchestrator for Your Infrastructure
            </p>
            <div className="space-x-4">
              <Button asChild className="group px-8 text-primary-foreground">
                <Link href="#getting-started">
                  Get Started
                  <ArrowRight className="ml-2 h-4 w-4 transition-transform duration-300 group-hover:translate-x-1" />
                </Link>
              </Button>
            </div>
          </div>
        </section>

        <section
          id="features"
          className="container space-y-6 bg-slate-50 py-8 dark:bg-transparent md:py-12 lg:py-24"
        >
          <div className="mx-auto flex max-w-[58rem] flex-col items-center space-y-4 text-center">
            <h2 className="font-heading text-3xl leading-[1.1] sm:text-3xl md:text-6xl">
              Features
            </h2>
            <p className="max-w-[85%] leading-normal text-muted-foreground sm:text-lg sm:leading-7">
              Powerful task scheduling and workflow orchestration for your
              infrastructure
            </p>
          </div>
          <div className="mx-auto grid justify-center gap-4 sm:grid-cols-2 md:max-w-[64rem] md:grid-cols-3">
            <div className="relative overflow-hidden rounded-lg border bg-background p-2">
              <div className="flex h-[180px] flex-col justify-between rounded-md p-6">
                <Clock className="h-12 w-12 text-primary" />
                <div className="space-y-2">
                  <h3 className="font-bold">Intuitive Scheduling</h3>
                  <p className="text-sm text-muted-foreground">
                    Schedule tasks with flexible intervals and precise timing
                    control.
                  </p>
                </div>
              </div>
            </div>
            <div className="relative overflow-hidden rounded-lg border bg-background p-2">
              <div className="flex h-[180px] flex-col justify-between rounded-md p-6">
                <Container className="h-12 w-12 text-primary" />
                <div className="space-y-2">
                  <h3 className="font-bold">Multiple Job Types</h3>
                  <p className="text-sm text-muted-foreground">
                    Support for health checks and containerized workloads.
                  </p>
                </div>
              </div>
            </div>
            <div className="relative overflow-hidden rounded-lg border bg-background p-2">
              <div className="flex h-[180px] flex-col justify-between rounded-md p-6">
                <Globe className="h-12 w-12 text-primary" />
                <div className="space-y-2">
                  <h3 className="font-bold">Distributed Execution</h3>
                  <p className="text-sm text-muted-foreground">
                    Run your tasks across distributed infrastructure for maximum
                    reliability.
                  </p>
                </div>
              </div>
            </div>
            <div className="relative overflow-hidden rounded-lg border bg-background p-2">
              <div className="flex h-[180px] flex-col justify-between rounded-md p-6">
                <Server className="h-12 w-12 text-primary" />
                <div className="space-y-2">
                  <h3 className="font-bold">High Availability</h3>
                  <p className="text-sm text-muted-foreground">
                    No downtime with our fault-tolerant architecture.
                  </p>
                </div>
              </div>
            </div>
            <div className="relative overflow-hidden rounded-lg border bg-background p-2">
              <div className="flex h-[180px] flex-col justify-between rounded-md p-6">
                <Shield className="h-12 w-12 text-primary" />
                <div className="space-y-2">
                  <h3 className="font-bold">Enterprise Security</h3>
                  <p className="text-sm text-muted-foreground">
                    Secure authentication and authorization for all your tasks.
                  </p>
                </div>
              </div>
            </div>
            <div className="relative overflow-hidden rounded-lg border bg-background p-2">
              <div className="flex h-[180px] flex-col justify-between rounded-md p-6">
                <Lock className="h-12 w-12 text-primary" />
                <div className="space-y-2">
                  <h3 className="font-bold">Advanced Monitoring</h3>
                  <p className="text-sm text-muted-foreground">
                    Real-time insights into your task execution and performance.
                  </p>
                </div>
              </div>
            </div>
          </div>
        </section>

        <section id="benefits" className="container space-y-6 py-8 md:py-12 lg:py-24">
          <div className="mx-auto flex max-w-[58rem] flex-col items-center space-y-4 text-center">
            <h2 className="font-heading text-3xl leading-[1.1] sm:text-3xl md:text-6xl">
              Why Choose Chronoverse?
            </h2>
            <span className="rounded-full bg-primary/10 px-4 py-1 text-sm font-medium text-primary">
              Free & Open Source
            </span>
            <p className="max-w-[85%] leading-normal text-muted-foreground sm:text-lg sm:leading-7">
              A community-driven project that&apos;s both powerful and completely free
            </p>
          </div>

          <div className="mx-auto grid max-w-4xl grid-cols-1 gap-4 md:grid-cols-2">
            <BenefitCard
              title="Self-Hosted & Private"
              description="Run Chronoverse in your own infrastructure, keeping full control of your data and workflows."
            />
            <BenefitCard
              title="Improve Reliability"
              description="Ensure critical tasks run on time, every time with our fault-tolerant system."
            />
            <BenefitCard
              title="Scale With Confidence"
              description="Our distributed architecture grows with your needs, and you can contribute to its development."
            />
            <BenefitCard
              title="Simplify Workflow Management"
              description="Manage complex task dependencies with our intuitive interface."
            />
          </div>
        </section>

        <section
          id="getting-started"
          className="container space-y-6 bg-slate-50 py-8 dark:bg-transparent md:py-12 lg:py-24"
        >
          <div className="mx-auto flex max-w-[58rem] flex-col items-center space-y-4 text-center">
            <h2 className="font-heading text-3xl leading-[1.1] sm:text-3xl md:text-6xl">
              Getting Started
            </h2>
            <p className="max-w-[85%] leading-normal text-muted-foreground sm:text-lg sm:leading-7">
              Deploy Chronoverse in your infrastructure in minutes
            </p>
          </div>

          <div className="mx-auto flex max-w-[58rem] flex-col items-center space-y-8 pt-6">
            <Button asChild size="lg" className="px-8">
              <Link
                href="https://github.com/hitesh22rana/chronoverse/blob/main/README.md#getting-started"
                target="_blank"
                rel="noopener noreferrer"
              >
                <FileCode className="mr-2 h-5 w-5" />
                View Deployment Guide
              </Link>
            </Button>
            <Button asChild variant="outline" size="lg">
              <Link
                href="https://github.com/hitesh22rana/chronoverse"
                target="_blank"
                rel="noopener noreferrer"
              >
                <GithubIcon />
                View on GitHub
              </Link>
            </Button>
          </div>
        </section>
      </main>

      <footer className="px-4 py-2">
        <div className="mx-auto container flex flex-col items-center justify-between gap-4 md:h-24 md:flex-row">
          <div className="flex flex-col items-center gap-4 px-8 md:flex-row md:gap-2 md:px-0">
            <p className="text-center text-sm leading-loose text-muted-foreground md:text-left">
              © 2025 Chronoverse. All rights reserved.
            </p>
          </div>
          <div className="flex gap-4">
            <Link
              href="https://github.com/hitesh22rana/chronoverse"
              className="text-sm text-muted-foreground hover:text-primary"
            >
              GitHub
            </Link>
            <Link
              href="https://github.com/hitesh22rana/chronoverse/blob/main/LICENSE"
              className="text-sm text-muted-foreground hover:text-primary"
            >
              License
            </Link>
            <Link
              href="https://github.com/hitesh22rana/chronoverse/issues"
              className="text-sm text-muted-foreground hover:text-primary"
            >
              Issues
            </Link>
          </div>
        </div>
      </footer>
    </Fragment>
  )
}

function BenefitCard({ title, description }: { title: string; description: string }) {
  return (
    <div className="flex items-start space-x-4 rounded-lg border bg-card p-6 text-card-foreground shadow-sm">
      <CheckCircle2 className="mt-1 h-6 w-6 flex-shrink-0 text-primary" />
      <div>
        <h3 className="mb-2 text-xl font-bold">{title}</h3>
        <p className="text-muted-foreground">{description}</p>
      </div>
    </div>
  )
}

function GithubIcon() {
  return (
    <svg
      role="img"
      viewBox="0 0 24 24"
      xmlns="http://www.w3.org/2000/svg"
      width="24"
      height="24"
      fill="currentColor"
    >
      <title>GitHub</title>
      <path d="M12 .297c-6.63 0-12 5.373-12 12 0 5.303 3.438 9.8 8.205 11.385.6.113.82-.258.82-.577 0-.285-.01-1.04-.015-2.04-3.338.724-4.042-1.61-4.042-1.61C4.422 18.07 3.633 17.7 3.633 17.7c-1.087-.744.084-.729.084-.729 1.205.084 1.838 1.236 1.838 1.236 1.07 1.835 2.809 1.305 3.495.998.108-.776.417-1.305.76-1.605-2.665-.3-5.466-1.332-5.466-5.93 0-1.31.465-2.38 1.235-3.22-.135-.303-.54-1.523.105-3.176 0 0 1.005-.322 3.3 1.23.96-.267 1.98-.399 3-.405 1.02.006 2.04.138 3 .405 2.28-1.552 3.285-1.23 3.285-1.23.645 1.653.24 2.873.12 3.176.765.84 1.23 1.91 1.23 3.22 0 4.61-2.805 5.625-5.475 5.92.42.36.81 1.096.81 2.22 0 1.606-.015 2.896-.015 3.286 0 .315.21.69.825.57C20.565 22.092 24 17.592 24 12.297c0-6.627-5.373-12-12-12" />
    </svg>
  )
}