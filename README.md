# Chronoverse ⏳🌌

**Distributed Task Scheduler & Orchestrator**

Chronoverse is a distributed, job scheduling and orchestration system designed for reliability and scalability. It allows you to define, schedule, and execute various types of jobs across your infrastructure with powerful monitoring and management capabilities.

[![Go Report Card](https://goreportcard.com/badge/github.com/hitesh22rana/chronoverse)](https://goreportcard.com/report/github.com/hitesh22rana/chronoverse) [![License](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

## Features

- **Workflow Management**:  Create, update, and monitor scheduled task workflows
- **Flexible Scheduling**: Configure jobs with precise time intervals in minutes
- **Multiple Workflow Types Support**: 
    - `HEARTBEAT`: Simple health check job
    - `CONTAINER`: Execute commands in Docker containers with configurable images and parameters
- **Job Logs**: Comprehensive execution history with logs stored in ClickHouse for efficient storage and retrieval
- **Real-time Notifications**: Dashboard-based alerts for workflow and job state changes
- **Observability**: Built-in OpenTelemetry integration for traces, metrics and logs
- **Security**: JWT-based authentication and authorization

## Architecture

Chronoverse implements a message-driven microservices architecture where components communicate through two primary channels:

1. **Kafka**: For reliable, asynchronous processing and event-driven workflows
2. **gRPC**: For efficient, low-latency synchronous service-to-service communication

This dual communication approach ensures both reliability for critical background processes and responsiveness for user-facing operations. Data persistence is handled by PostgreSQL for transactional data and ClickHouse for analytics and high-volume job logs.

### Core Services

- **Server**: HTTP API gateway exposing RESTful endpoints with middleware for authentication and authorization
- **Users Service**: Manages user accounts, authentication tokens, and notification preferences
- **Workflows Service**: Handles workflow definitions, configuration storage, and build status management
- **Jobs Service**: Manages job lifecycle from scheduling through execution and completion
- **Notifications Service**: Provides real-time alerts and status updates

### Worker Components

- **Scheduling Worker**: Identifies jobs due for execution based on their schedules and processes them through Kafka
- **Workflow Worker**: Builds Docker image configurations from workflow definitions and prepares execution templates
- **Execution Worker**: Executes scheduled jobs in isolated containers with proper resource management, manages execution lifecycle and captures outputs/logs
- **JobLogs Processor**: Performs efficient batch insertion of execution logs from Kafka to ClickHouse for persistent storage and optimized querying

All workers communicate through Kafka topics, enabling horizontal scaling and fault tolerance. This message-driven architecture ensures that job processing can continue even if individual components experience temporary outages.

## Getting Started

### Prerequisites
- **Docker**: [Install Docker](https://docs.docker.com/get-docker/)
- **Docker Compose**: [Install Docker Compose](https://docs.docker.com/compose/install/)

No other dependencies are required as all services run in containers with automated setup.

### Installation

1. Clone the repository:
   ```
   git clone https://github.com/hitesh22rana/chronoverse.git
   cd chronoverse
   ```

2. Choose the appropriate deployment environment:
   **For Development:**
   ```
   docker compose -f compose.dev.yaml up -d
   ```

   **For Production:**
   ```
   docker compose -f compose.prod.yaml up -d
   ```

### Deployment Environments

#### 1. Development Environment (compose.dev.yaml)
- All service ports are exposed for easy debugging and direct access
- Database ports (5432, 9000, 6379) accessible from the host
- gRPC service ports (50051-50054) available for direct testing
- Monitoring ports fully exposed
- Suitable for local development and testing

#### 2. Production Environment (compose.prod.yaml)
- Enhanced security with minimal port exposure
- Only the main application server (port 8080) is exposed externally
- Monitoring UI (port 3000) is restricted to localhost access
- All internal services communicate via Docker's internal network
- No direct external access to databases or internal microservices
- gRPC reflection disabled for additional security

### Configuration

Configuration is managed through environment variables with sensible defaults in the [docker-compose.yaml](./docker-compose.yaml) file.

## License

This project is licensed under the MIT License - see the [LICENSE](./LICENSE) file for details.

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.

1. Fork the repository
2. Create your feature branch (`git checkout -b feature/amazing-feature`)
3. Commit your changes (`git commit -m 'Add some amazing feature'`)
4. Push to the branch (`git push origin feature/amazing-feature`)
5. Open a Pull Request

## Acknowledgments

- [Franz-go](https://github.com/twmb/franz-go) - Kafka client
- [Docker SDK for Go](https://github.com/moby/moby)
- [OpenTelemetry Go](https://github.com/open-telemetry/opentelemetry-go)
- [Docker OTEL lgtm](https://github.com/grafana/docker-otel-lgtm)

---

Built with ❤️ by [Hitesh Rana](https://github.com/hitesh22rana)