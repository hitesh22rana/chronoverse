import {
  Activity,
  ArrowRight,
  Bell,
  Boxes,
  Braces,
  ChartNoAxesCombined,
  CheckCircle2,
  Container,
  Database,
  FileSearch,
  KeyRound,
  RadioTower,
  RefreshCw,
  ScrollText,
  ShieldCheck,
  Terminal,
  TimerReset,
  Workflow,
} from "lucide-react";
import Image from "next/image";
import Link from "next/link";

import { ArchitectureMap } from "@/components/architecture-map";
import { GitHubMark } from "@/components/github-mark";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Separator } from "@/components/ui/separator";
import { REPOSITORY_URL, withBasePath } from "@/lib/site";

const capabilities = [
  { icon: Workflow, title: "Scheduled workflows", text: "Run interval-based workloads with generation guards and asynchronous build preparation." },
  { icon: TimerReset, title: "Manual runs", text: "Dispatch immediate jobs through the same replay-safe lifecycle as automatic scheduling." },
  { icon: Container, title: "Container execution", text: "Execute Docker-backed commands through a constrained socket proxy and durable job ownership." },
  { icon: Activity, title: "Heartbeat checks", text: "Model lightweight service checks separately from log-producing container workloads." },
  { icon: ScrollText, title: "Live logs", text: "Stream running stdout and stderr to the dashboard over Server-Sent Events." },
  { icon: FileSearch, title: "Retained search", text: "Store logs in ClickHouse, search with Meilisearch, and download safely filtered output." },
  { icon: ChartNoAxesCombined, title: "Analytics", text: "Track workflow and job totals, log volume, and accumulated execution duration." },
  { icon: Bell, title: "Notifications", text: "Surface workflow and job state changes with replay-safe notification identity." },
];

const reliability = [
  ["Idempotency keys", "Retry the same command without duplicating a workflow or manual job."],
  ["Transactional outbox", "Commit domain state and publication intent in one PostgreSQL transaction."],
  ["Workflow generations", "Reject build, schedule, termination, and deletion work from old definitions."],
  ["Durable job leases", "Prevent stale workers from completing work after ownership has moved."],
  ["Deterministic events", "Deduplicate notifications, analytics, and retained log side effects."],
  ["Partition commit policy", "Advance Kafka offsets only after the selected success or retry outcome."],
];

const timeline = [
  ["01", "Command", "The gateway validates session, CSRF state, input, and idempotency."],
  ["02", "Commit", "The domain service writes state and an outbox event atomically."],
  ["03", "Dispatch", "The relay publishes to Kafka and partition workers take ownership."],
  ["04", "Execute", "A worker claims a lease, runs the container, and renews ownership."],
  ["05", "Observe", "Logs, notifications, analytics, traces, and terminal state converge."],
];

export default function Home() {
  return (
    <main>
      <section className="hero section-shell">
        <div className="hero-copy">
          <Badge className="hero-status" variant="outline"><span className="status-dot" />Self-hosted orchestration</Badge>
          <h1>Reliable scheduled work, on infrastructure you control.</h1>
          <p>Chronoverse is a distributed scheduler for heartbeat checks and container workloads, built around durable state, replay-safe events, searchable logs, and observable execution.</p>
          <div className="hero-actions">
            <Button asChild size="lg"><Link href="/docs/quickstart">Read the docs<ArrowRight data-icon="inline-end" /></Link></Button>
            <Button asChild size="lg" variant="outline"><a href={REPOSITORY_URL} target="_blank" rel="noreferrer"><GitHubMark data-icon="inline-start" />View source</a></Button>
          </div>
          <div className="hero-command"><Terminal /><code>docker compose -f compose.dev.yaml up -d</code></div>
        </div>
        <div className="hero-visual" aria-label="Chronoverse engineering status panel">
          <Image src={withBasePath("/assets/chronoverse.png")} alt="Chronoverse astronaut with an hourglass visor" width={1536} height={1024} priority />
          <div className="hero-console">
            <div><span>workflow.build</span><strong>completed</strong></div>
            <div><span>job.lease</span><strong>renewed · 30s</strong></div>
            <div><span>outbox.publish</span><strong>kafka/jobs</strong></div>
            <div><span>trace.context</span><strong>propagated</strong></div>
          </div>
        </div>
      </section>

      <section className="signal-strip" aria-label="Platform summary">
        <div><strong>5</strong><span>gRPC domains</span></div>
        <div><strong>7</strong><span>worker roles</span></div>
        <div><strong>4</strong><span>Kafka topics</span></div>
        <div><strong>6</strong><span>infrastructure systems</span></div>
      </section>

      <section className="section-shell landing-section" id="product">
        <div className="section-heading"><Badge variant="secondary">Product</Badge><h2>One lifecycle from schedule to evidence.</h2><p>Define work once, run it automatically or on demand, and keep the operational trail needed to understand what happened.</p></div>
        <div className="capability-grid">
          {capabilities.map(({ icon: Icon, title, text }) => (
            <Card key={title}>
              <CardHeader><span className="feature-icon"><Icon /></span><CardTitle>{title}</CardTitle></CardHeader>
              <CardContent><CardDescription>{text}</CardDescription></CardContent>
            </Card>
          ))}
        </div>
      </section>

      <section className="engineering-section" id="engineering">
        <div className="section-shell landing-section">
          <div className="section-heading section-heading-wide"><Badge variant="secondary">Engineering</Badge><h2>Synchronous ownership. Asynchronous progress.</h2><p>The public API stays responsive through gRPC domain boundaries while Kafka workers absorb delayed, distributed, and failure-prone execution.</p></div>
          <ArchitectureMap />

          <div className="engineering-split">
            <div>
              <div className="eyebrow"><RadioTower /> Event flow</div>
              <h3>Every stage has an owner and a recovery path.</h3>
            </div>
            <ol className="execution-timeline">
              {timeline.map(([number, title, text]) => <li key={number}><span>{number}</span><div><strong>{title}</strong><p>{text}</p></div></li>)}
            </ol>
          </div>

          <Separator />

          <div className="reliability-grid">
            <div className="reliability-intro">
              <div className="eyebrow"><RefreshCw /> Reliability model</div>
              <h3>Failure is represented in state, not hidden behind retries.</h3>
              <p>Chronoverse assumes messages repeat, processes restart, and ownership expires. Correctness comes from explicit invariants at every boundary.</p>
              <Button asChild variant="outline"><Link href="/docs/engineering/replay-safety">Explore replay safety<ArrowRight data-icon="inline-end" /></Link></Button>
            </div>
            <div className="reliability-list">
              {reliability.map(([title, text]) => <div key={title}><CheckCircle2 /><span><strong>{title}</strong><p>{text}</p></span></div>)}
            </div>
          </div>

          <div className="infra-grid">
            <Card><CardHeader><Database /><CardTitle>PostgreSQL</CardTitle></CardHeader><CardContent><CardDescription>Transactional state, idempotency, outbox rows, leases, retries, and analytics.</CardDescription></CardContent></Card>
            <Card><CardHeader><Boxes /><CardTitle>ClickHouse + Meilisearch</CardTitle></CardHeader><CardContent><CardDescription>Ordered retained output with low-latency text search and safe highlights.</CardDescription></CardContent></Card>
            <Card><CardHeader><KeyRound /><CardTitle>Redis</CardTitle></CardHeader><CardContent><CardDescription>Sessions, cached reads, live log delivery, and shared-host image-pull coordination.</CardDescription></CardContent></Card>
            <Card><CardHeader><ShieldCheck /><CardTitle>TLS + OpenTelemetry</CardTitle></CardHeader><CardContent><CardDescription>mTLS service communication and trace propagation across HTTP, gRPC, and Kafka.</CardDescription></CardContent></Card>
          </div>
        </div>
      </section>

      <section className="section-shell landing-section" id="operations">
        <div className="section-heading"><Badge variant="secondary">Operations</Badge><h2>Built to be inspected while it runs.</h2><p>Health checks establish startup order. LGTM receives traces, metrics, and logs. Recovery loops make abandoned work visible and bounded.</p></div>
        <div className="operations-panel">
          <div className="operations-code">
            <span>$ docker compose -f compose.prod.yaml ps</span>
            <code>outbox-relay       running   healthy</code>
            <code>execution-worker   running   2/2 replicas</code>
            <code>joblogs-processor  running   healthy</code>
            <code>server             running   healthy</code>
          </div>
          <div className="operations-links">
            <Link href="/docs/operations/monitoring"><strong>Monitoring</strong><span>Health signals and first response</span><ArrowRight /></Link>
            <Link href="/docs/operations/scaling"><strong>Scaling</strong><span>Partitions, replicas, and capacity</span><ArrowRight /></Link>
            <Link href="/docs/operations/troubleshooting"><strong>Troubleshooting</strong><span>TLS, readiness, logs, and SSE</span><ArrowRight /></Link>
          </div>
        </div>
      </section>

      <section className="section-shell docs-cta">
        <div>
          <Braces />
          <span>46 authored guides · 23 generated API operations · static HTML</span>
        </div>
        <h2>The engineering reference lives with the code.</h2>
        <p>MDX content, OpenAPI contracts, navigation, validation, and search are built in this repository and deployed with the landing page.</p>
        <div className="hero-actions"><Button asChild size="lg"><Link href="/docs">Open documentation<ArrowRight data-icon="inline-end" /></Link></Button><Button asChild size="lg" variant="outline"><Link href="/docs/api/reference">Browse the API</Link></Button></div>
      </section>
    </main>
  );
}
