import { ArrowRight, Database, RadioTower, Server, Workflow } from "lucide-react";

const services = ["users", "workflows", "jobs", "notifications", "analytics"];
const workers = ["scheduler", "workflow", "execution", "job logs", "analytics", "outbox"];
const stores = ["PostgreSQL", "ClickHouse", "Redis", "Meilisearch", "Docker proxy", "LGTM"];

export function ArchitectureMap() {
  return (
    <figure className="architecture-map" aria-labelledby="architecture-caption">
      <div className="architecture-flow">
        <div className="architecture-lane architecture-entry">
          <span className="architecture-icon"><Server /></span>
          <div><strong>HTTP gateway</strong><small>Sessions · CSRF · REST · SSE</small></div>
        </div>
        <ArrowRight className="architecture-arrow" aria-hidden="true" />
        <div className="architecture-column">
          <div className="architecture-label"><Workflow /> gRPC domains</div>
          <div className="architecture-chips">{services.map((service) => <span key={service}>{service}</span>)}</div>
        </div>
        <ArrowRight className="architecture-arrow" aria-hidden="true" />
        <div className="architecture-column architecture-bus">
          <div className="architecture-label"><RadioTower /> Kafka workers</div>
          <div className="architecture-chips">{workers.map((worker) => <span key={worker}>{worker}</span>)}</div>
        </div>
        <ArrowRight className="architecture-arrow" aria-hidden="true" />
        <div className="architecture-column">
          <div className="architecture-label"><Database /> infrastructure</div>
          <div className="architecture-chips">{stores.map((store) => <span key={store}>{store}</span>)}</div>
        </div>
      </div>
      <figcaption id="architecture-caption">Synchronous domain ownership with asynchronous, replay-safe orchestration.</figcaption>
    </figure>
  );
}
