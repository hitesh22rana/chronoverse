import {
  Activity,
  ArrowRight,
  Bell,
  Boxes,
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
import Link from "next/link";

import { ArchitectureMap } from "@/components/architecture-map";
import { GitHubMark } from "@/components/github-mark";
import { JsonLd } from "@/components/seo/json-ld";
import { Accordion, AccordionContent, AccordionItem, AccordionTrigger } from "@/components/ui/accordion";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Separator } from "@/components/ui/separator";
import {
  AUTHOR_IMAGE,
  AUTHOR_NAME,
  AUTHOR_PROFILES,
  AUTHOR_URL,
  CONTACT_EMAIL,
  REPOSITORY_URL,
  SITE_PUBLISHED,
  SITE_URL,
  SOCIAL_IMAGE_ALT,
  SOCIAL_IMAGE_URL,
  siteAssetUrl,
  sitePageUrl,
  withBasePath,
} from "@/lib/site";

const capabilities = [
  { icon: Workflow, title: "Scheduled workflows", text: "Run interval workloads with generation guards. Build prep runs in the background." },
  { icon: TimerReset, title: "Manual runs", text: "Run jobs now through the same replay-safe path as a scheduled run." },
  { icon: Container, title: "Container execution", text: "Run Docker commands on the owning node, with health checks and a constrained socket proxy." },
  { icon: Activity, title: "Heartbeat checks", text: "Run lightweight service checks. They stay apart from container jobs that keep logs." },
  { icon: ScrollText, title: "Live logs", text: "Stream running stdout and stderr to the dashboard over Server-Sent Events." },
  { icon: FileSearch, title: "Retained search", text: "Store logs in ClickHouse, search with Meilisearch, and download safely filtered output." },
  { icon: ChartNoAxesCombined, title: "Analytics", text: "Track workflow and job totals, log volume, and time spent running." },
  { icon: Bell, title: "Notifications", text: "Alert when workflow or job state changes. Each alert has a replay-safe ID." },
];

const reliability = [
  ["Idempotency keys", "Retry the same command without duplicating a workflow or manual job."],
  ["Transactional outbox", "Commit domain state and publication intent in one PostgreSQL transaction."],
  ["Workflow generations", "Reject build, schedule, terminate, and delete work from an old definition."],
  ["Durable job leases", "Stop stale workers from finishing work after ownership has moved."],
  ["Deterministic events", "Deduplicate notifications, analytics, and retained logs."],
  ["Partition commit policy", "Advance Kafka offsets only after the final result is known."],
];

const timeline = [
  ["01", "Command", "The gateway validates session, CSRF state, input, and idempotency."],
  ["02", "Commit", "The service writes state and an outbox event in one transaction."],
  ["03", "Dispatch", "The relay publishes to Kafka and partition workers take ownership."],
  ["04", "Execute", "A worker claims a lease and takes a runtime endpoint, then runs the container and renews the lease."],
  ["05", "Observe", "Logs, notifications, analytics, traces, and terminal state converge."],
];

const lastUpdated = new Date();

const author = {
  "@type": "Person",
  "@id": `${AUTHOR_URL}#person`,
  name: AUTHOR_NAME,
  url: AUTHOR_URL,
  image: AUTHOR_IMAGE,
  email: CONTACT_EMAIL,
  jobTitle: "Maintainer",
  sameAs: AUTHOR_PROFILES.map((profile) => profile.url),
};

const organization = {
  "@type": "Organization",
  "@id": `${SITE_URL}/#organization`,
  name: "Chronoverse",
  url: SITE_URL,
  logo: siteAssetUrl("/assets/chronoverse-mark.webp"),
  founder: author["@id"],
  email: CONTACT_EMAIL,
  sameAs: [REPOSITORY_URL, ...AUTHOR_PROFILES.map((profile) => profile.url)],
  contactPoint: {
    "@type": "ContactPoint",
    contactType: "technical support",
    email: CONTACT_EMAIL,
    url: SITE_URL,
    availableLanguage: ["en"],
  },
};

const faq = [
  {
    question: "What does Chronoverse actually run?",
    answer: "Two workflow kinds. A HEARTBEAT workflow is a lightweight service check that produces no logs. A CONTAINER workflow runs a Docker workload and can retain its stdout and stderr. Both can run on an interval or be dispatched by hand, and both go through the same replay-safe lifecycle.",
  },
  {
    question: "How long does a worker hold a job?",
    answer: "An execution worker claims a lease and renews it every 30s by default. That lease is what stops a stale worker from finishing work after ownership has moved, which matters when a process is paused, partitioned or restarted mid job.",
  },
  {
    question: "How long are failed commands remembered?",
    answer: "Command idempotency records are kept for 336h, and the setting cannot go below 168h. It has to cover the longest Kafka, published outbox or manual redrive window, or a retried command could run twice.",
  },
  {
    question: "What does it need to run?",
    answer: "Docker for local work and Kubernetes for production. Go services and workers sit behind an HTTP and gRPC API, with Kafka for events, PostgreSQL for state, Redis for sessions and live logs, and ClickHouse with Meilisearch for retained search.",
  },
  {
    question: "How is it licensed?",
    answer: "MIT. You run it on your own machines, and you keep the data it produces. There is no hosted tier and no account to create.",
  },
];

const structuredData = {
  "@context": "https://schema.org",
  "@graph": [
    {
      "@type": "SoftwareSourceCode",
      "@id": `${SITE_URL}/#software`,
      name: "Chronoverse",
      description: "A self-hosted distributed scheduler for heartbeat checks and container workloads with replay-safe execution, searchable logs, and full observability.",
      url: sitePageUrl(),
      codeRepository: REPOSITORY_URL,
      license: "https://opensource.org/license/mit",
      programmingLanguage: ["Go", "TypeScript"],
      runtimePlatform: ["Docker", "Kubernetes"],
      datePublished: SITE_PUBLISHED,
      dateModified: lastUpdated.toISOString(),
      author: { "@id": author["@id"] },
      maintainer: { "@id": organization["@id"] },
    },
    {
      "@type": "WebPage",
      "@id": `${SITE_URL}/#webpage`,
      name: "Chronoverse",
      description: "Reliable scheduled work, on infrastructure you control.",
      url: sitePageUrl(),
      isPartOf: { "@id": `${SITE_URL}/#website` },
      primaryImageOfPage: { "@id": `${SITE_URL}/#primaryimage` },
      datePublished: SITE_PUBLISHED,
      dateModified: lastUpdated.toISOString(),
      author: { "@id": author["@id"] },
      inLanguage: "en",
    },
    {
      "@type": "WebSite",
      "@id": `${SITE_URL}/#website`,
      name: "Chronoverse",
      url: SITE_URL,
      description: "Documentation and project site for Chronoverse, a self-hosted distributed scheduler.",
      publisher: { "@id": organization["@id"] },
      inLanguage: "en",
    },
    {
      "@type": "ImageObject",
      "@id": `${SITE_URL}/#primaryimage`,
      url: SOCIAL_IMAGE_URL,
      contentUrl: SOCIAL_IMAGE_URL,
      width: 1200,
      height: 630,
      caption: SOCIAL_IMAGE_ALT,
    },
    {
      "@type": "FAQPage",
      "@id": `${SITE_URL}/#faq`,
      mainEntity: faq.map(({ question, answer }) => ({
        "@type": "Question",
        name: question,
        acceptedAnswer: { "@type": "Answer", text: answer },
      })),
    },
    {
      "@type": "BreadcrumbList",
      "@id": `${SITE_URL}/#breadcrumb`,
      itemListElement: [{ "@type": "ListItem", position: 1, name: "Chronoverse", item: sitePageUrl() }],
    },
    author,
    organization,
  ],
};

export default function Home() {
  return (
    <main>
      <JsonLd data={structuredData} />
      <section className="hero section-shell">
        <div className="hero-copy">
          <Badge className="hero-status" variant="outline"><span className="status-dot" />Self-hosted orchestration</Badge>
          <h1>Reliable scheduled work, on infrastructure you control.</h1>
          <p>Chronoverse is a self-hosted scheduler for heartbeat checks and container workloads. Go services and Kafka workers sit behind it, state lives in PostgreSQL, and every run leaves a trail you can inspect.</p>
          <div className="hero-actions">
            <Button asChild size="lg"><Link href="/docs/quickstart">Read the docs<ArrowRight data-icon="inline-end" /></Link></Button>
            <Button asChild size="lg" variant="outline"><a href={REPOSITORY_URL} target="_blank" rel="noreferrer"><GitHubMark data-icon="inline-start" />View source</a></Button>
          </div>
          <div className="hero-command"><Terminal /><code>scripts/k8s/setup.sh --mode production --context &lt;context&gt;</code></div>
        </div>
        <div className="hero-visual" aria-label="Chronoverse engineering status panel">
            {/* eslint-disable-next-line @next/next/no-img-element -- srcset needs hand-written candidates because the static export disables the image optimiser */}
            <img
              src={withBasePath("/assets/chronoverse.webp")}
              srcSet={`${withBasePath("/assets/chronoverse-768.webp")} 768w, ${withBasePath("/assets/chronoverse-1152.webp")} 1152w, ${withBasePath("/assets/chronoverse.webp")} 1536w`}
              sizes="(max-width: 850px) 100vw, 46vw"
              alt="Chronoverse astronaut with an hourglass visor"
              width={1536}
              height={1024}
              fetchPriority="high"
              decoding="async"
            />
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
        <div><strong>6</strong><span>worker roles</span></div>
        <div><strong>4</strong><span>Kafka topics</span></div>
        <div><strong>1+</strong><span>runtime nodes</span></div>
      </section>

      <section className="section-shell landing-section" id="product">
        <div className="section-heading"><Badge variant="secondary">Product</Badge><h2>One lifecycle from schedule to evidence.</h2><p>Define work once, run it on a schedule or on demand, and keep the trail you need to explain what happened.</p></div>
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
          <div className="section-heading section-heading-wide"><Badge variant="secondary">Engineering</Badge><h2>Synchronous ownership. Asynchronous progress.</h2><p>The API stays responsive across gRPC domain boundaries. Kafka workers route slow, distributed, and failure-prone work to the Docker nodes they own.</p></div>
          <ArchitectureMap />

          <div className="engineering-split">
            <div>
              <div className="eyebrow"><RadioTower /> Event flow</div>
              <h3>Who owns each of the 5 stages, and where does recovery start?</h3>
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
              <p>Chronoverse assumes messages repeat, processes restart, and ownership expires. Explicit invariants keep it correct at every boundary, and idempotency records outlive the longest redrive window.</p>
              <Button asChild variant="outline"><Link href="/docs/engineering/replay-safety">Explore replay safety<ArrowRight data-icon="inline-end" /></Link></Button>
            </div>
            <div className="reliability-list">
              {reliability.map(([title, text]) => <div key={title}><CheckCircle2 /><span><strong>{title}</strong><p>{text}</p></span></div>)}
            </div>
          </div>

          <div className="infra-grid">
            <Card><CardHeader><Database /><CardTitle>PostgreSQL</CardTitle></CardHeader><CardContent><CardDescription>Transactional state, idempotency, outbox rows, leases, retries, and analytics.</CardDescription></CardContent></Card>
            <Card><CardHeader><Boxes /><CardTitle>ClickHouse + Meilisearch</CardTitle></CardHeader><CardContent><CardDescription>Ordered retained output with low-latency text search and safe highlights.</CardDescription></CardContent></Card>
<Card><CardHeader><KeyRound /><CardTitle>Redis</CardTitle></CardHeader><CardContent><CardDescription>Sessions, cached reads, live log delivery, and image pulls scoped to each runtime node.</CardDescription></CardContent></Card>
          <Card><CardHeader><ShieldCheck /><CardTitle>TLS + OpenTelemetry</CardTitle></CardHeader><CardContent><CardDescription>mTLS between services, and trace propagation across HTTP, gRPC and Kafka.</CardDescription></CardContent></Card>

          </div>
        </div>
      </section>

      <section className="section-shell landing-section" id="operations">
        <div className="section-heading"><Badge variant="secondary">Operations</Badge><h2>Can you inspect it while it runs?</h2><p>Startup order comes from health checks. LGTM receives traces, metrics and logs. Recovery loops make abandoned work visible and bounded.</p></div>
        <div className="operations-panel">
          <div className="operations-code">
            <span>$ kubectl -n chronoverse get deploy,ds</span>
            <code>outbox-relay       2/2 available</code>
            <code>execution-worker   2/2 available</code>
            <code>docker-proxy ds    runtime-agent sidecars ready</code>
            <code>joblogs-processor  2/2 available</code>
            <code>server             2/2 available</code>
          </div>
          <div className="operations-links">
            <Link href="/docs/operations/monitoring"><strong>Monitoring</strong><span>Health signals and first response</span><ArrowRight /></Link>
            <Link href="/docs/operations/scaling"><strong>Scaling</strong><span>Partitions, replicas, and capacity</span><ArrowRight /></Link>
            <Link href="/docs/operations/troubleshooting"><strong>Troubleshooting</strong><span>TLS, readiness, logs, and SSE</span><ArrowRight /></Link>
          </div>
        </div>
      </section>

      <section className="section-shell docs-cta">
        <Badge variant="secondary">Documentation</Badge>
        <h2>Where does the engineering reference live?</h2>
        <p>The guides, the OpenAPI contract, the navigation, the link checks and this site are all authored in this repository and deployed together.</p>
        <div className="hero-actions"><Button asChild size="lg"><Link href="/docs">Open documentation<ArrowRight data-icon="inline-end" /></Link></Button><Button asChild size="lg" variant="outline"><Link href="/docs/api/reference">Browse the API</Link></Button></div>
      </section>

      <Separator className="section-rule" />
      <section className="section-shell landing-section faq-section" id="faq">
        <div className="section-heading"><Badge variant="secondary">FAQ</Badge><h2>Questions readers ask first.</h2><p>Short answers, taken from the configuration reference and the source.</p></div>
        <Accordion className="faq-accordion" collapsible defaultValue="lease" type="single">
          {faq.map(({ question, answer }, index) => (
            <AccordionItem key={question} value={["lease", "retention", "runs", "needs", "licence"][index]}>
              <AccordionTrigger>{question}</AccordionTrigger>
              <AccordionContent>{answer}</AccordionContent>
            </AccordionItem>
          ))}
        </Accordion>
      </section>
    </main>
  );
}
