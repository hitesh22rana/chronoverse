# Chronoverse ⏳🌌

**Distributed Task Scheduler & Orchestrator**

Chronoverse is a distributed, job scheduling and orchestration system designed for reliability and scalability. It allows you to define, schedule, and execute various types of jobs across your infrastructure with powerful monitoring and management capabilities.

[![Go Report Card](https://goreportcard.com/badge/github.com/hitesh22rana/chronoverse)](https://goreportcard.com/report/github.com/hitesh22rana/chronoverse) [![License](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

## Features

- **Workflow Management**: Create, update, and manage scheduled tasks
- **Flexible Scheduling**: Schedule jobs with cron expressions and precise intervals
- **Multiple Workflow Types Support**: 
    - `HEARTBEAT`: Simple health check jobs
    - `CONTAINER`: Execute commands in Docker containers
- **Distributed Architecture**: Microservices-based design for scalability
- **High Availability**: Fault-tolerant operation with no single point of failure
- **Notification System**: Real-time alerts and status updates
- **Observability**: Built-in OpenTelemetry integration for tracing and metrics
- **Security**: JWT-based authentication and authorization

## Architecture

Chronoverse follows a microservices architecture with the following components:

### Core Services

- **Server**: API gateway that handles client interactions and routes requests to appropriate services
- **Users Service**: Manages user authentication, authorization, and user profile data
- **Workflows Service**: Enables creation, configuration, and management of workflow definitions
- **Jobs Service**: Handles scheduled job instances and their lifecycle management
- **Notifications Service**: Delivers real-time alerts and status updates

### Worker Components

- **Scheduling Worker**: Identifies jobs due for execution based on their schedules and initiates the execution process
- **Workflow Worker**: Prepares jobs for execution, handling any build steps required before execution
- **Execution Worker**: Executes scheduled jobs in isolated containers with proper resource management

Each component communicates through message queuing to ensure reliability and scalability in distributed environments.

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

2. Run the entire stack with Docker Compose:
   ```
   docker-compose up -d
   ```

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