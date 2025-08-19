# Chronoverse ⏳🌌

![chronoverse](./.github/assets/chronoverse.png)

**Distributed job scheduler & Orchestrator on your infrastructure**

Chronoverse is a distributed job scheduling and orchestration system designed for reliability and scalability. It allows you to define, schedule, and execute various types of jobs across your infrastructure with powerful monitoring and management capabilities.

[![Go Report Card](https://goreportcard.com/badge/github.com/hitesh22rana/chronoverse)](https://goreportcard.com/report/github.com/hitesh22rana/chronoverse) [![License](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE) [![Ask DeepWiki](https://deepwiki.com/badge.svg)](https://deepwiki.com/hitesh22rana/chronoverse)

## Features

- **Workflow Management**: Create, update, and monitor scheduled workflows.
- **Flexible Scheduling**: Configure workflows with precise time intervals in minutes.
- **Multiple Workflow Types Support**:
  - `HEARTBEAT`: Simple health check job.
  - `CONTAINER`: Execute custom containerized applications and scripts.
- **Job Logs**: Comprehensive execution history with logs stored in ClickHouse for efficient storage and retrieval.
- **Live Log Streaming**: Real-time job log streaming using Server-Sent Events (SSE) for running jobs with automatic fallback to static logs for completed jobs.
- **Real-time Notifications**: Dashboard-based alerts for workflow and job state changes.
- **Observability**: Built-in OpenTelemetry integration for traces, metrics and logs.
- **Security**: JWT-based authentication and authorization.

## Architecture

Chronoverse implements a message-driven microservices architecture where components communicate through two primary channels:

1. **Kafka**: For reliable, asynchronous processing and event-driven workflows.
2. **gRPC**: For efficient, low-latency synchronous service-to-service communication.

This dual communication approach ensures both reliability for critical background processes and responsiveness for user-facing operations. Data persistence is handled by PostgreSQL for transactional data and ClickHouse for analytics and high-volume job logs.

### Core Services

- **Server**: HTTP API gateway exposing RESTful endpoints with middleware for authentication and authorization.
- **Users Service**: Manages user accounts, authentication tokens, and notification preferences.
- **Workflows Service**: Handles workflow definitions, configuration storage, and build status management.
- **Jobs Service**: Manages job lifecycle from scheduling through execution and completion.
- **Notifications Service**: Provides real-time alerts and status updates.
- **Analytics Service**: Provides insights into job and workflow performance and trends.

### Worker Components

- **Scheduling Worker**: Identifies jobs due for execution based on their schedules and processes them through Kafka.
- **Workflow Worker**: Builds Docker image configurations from workflow definitions and prepares execution templates.
- **Execution Worker**: Executes scheduled jobs in isolated containers with proper resource management, manages execution lifecycle and captures outputs/logs.
- **JobLogs Processor**: Performs efficient batch insertion of execution logs from Kafka to ClickHouse for persistent storage and optimized querying.
- **Analytics Processor**: Consumes job and workflow events from Kafka, processes them to generate analytics data, and stores the results for querying.
- **Database Migration**: Manages database schema evolution and applies necessary migrations for PostgreSQL and ClickHouse during deployments or updates.

All workers communicate through Kafka topics, enabling horizontal scaling and fault tolerance. This message-driven architecture ensures that job processing can continue even if individual components experience temporary outages.

## Getting Started

### Prerequisites

- **Docker**: [Install Docker](https://docs.docker.com/get-docker/)
- **Docker Compose**: [Install Docker Compose](https://docs.docker.com/compose/install/)

No other dependencies are required as all services run in containers with automated setup.

### Installation

1. Clone the repository:

   ```bash
   git clone https://github.com/hitesh22rana/chronoverse.git
   cd chronoverse
   ```

2. **For production deployment, see the [infra/](./infra/) directory for deployment options.**

## 🚀 Production Deployment

Chronoverse supports multiple production deployment methods. **For production deployments, refer to the [infra/](./infra/) directory.**

### Quick Start Options

#### 🐳 **Docker Compose** (Single-server production)
```bash
cd infra/
docker compose -f compose.prod.yaml up -d
```

#### ☸️ **Kubernetes** (Distributed, scalable production)
```bash
cd infra/k8s/
./deploy.sh --local    # Local development
./deploy.sh            # Cloud production
```

### 🤔 **Which Deployment to Choose?**

| Use Case | Recommended | Documentation |
|----------|------------|---------------|
| **Small-medium production** (< 1000 users) | 🐳 Docker Compose | [infra/README.md](./infra/README.md) |
| **Enterprise production** (1000+ users) | ☸️ Kubernetes | [infra/k8s/README.md](./infra/k8s/README.md) |
| **Local development** | 🐳 Docker Compose | [compose.dev.yaml](./compose.dev.yaml) |

**👉 See [infra/README.md](./infra/README.md) for detailed comparison and decision guidance.**

## 🛠️ Development Setup

For local development and testing:

```bash
# Start development environment
docker compose -f compose.dev.yaml up -d

# Access the dashboard
http://localhost:3001

# Access monitoring
http://localhost:3000
```

**Development Features:**
- All service ports exposed for debugging
- Database ports (5432, 9000, 6379) accessible from host  
- gRPC service ports (50051-50055) available for testing
- Hot-reload enabled for faster development

### Configuration

Configuration is managed through environment variables with sensible defaults in the [docker-compose.yaml](./docker-compose.yaml) file.

## License

This project is licensed under the MIT License - see the [LICENSE](./LICENSE) file for details.

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.

1. Fork the repository.
2. Create your feature branch (`git checkout -b feature/amazing-feature`).
3. Commit your changes (`git commit -m 'Add some amazing feature'`).
4. Push to the branch (`git push origin feature/amazing-feature`).
5. Open a Pull Request.

## Acknowledgments

- [Franz-go](https://github.com/twmb/franz-go) - Kafka client
- [Docker SDK for Go](https://github.com/moby/moby)
- [OpenTelemetry Go](https://github.com/open-telemetry/opentelemetry-go)
- [Docker OTEL lgtm](https://github.com/grafana/docker-otel-lgtm)

---

Built with ❤️ by [Hitesh Rana](https://github.com/hitesh22rana)