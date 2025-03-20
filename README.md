# Chronoverse ⏳🌌

**Distributed Task Scheduler & Orchestrator**

Chronoverse is a distributed, job scheduling and orchestration system designed for reliability and scalability. It allows you to define, schedule, and execute various types of jobs across your infrastructure with powerful monitoring and management capabilities.

[![Go Report Card](https://goreportcard.com/badge/github.com/hitesh22rana/chronoverse)](https://goreportcard.com/report/github.com/hitesh22rana/chronoverse) [![License](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

## Features

- **Job Management**: Create, update, and manage scheduled tasks
- **Flexible Scheduling**: Schedule jobs with cron expressions and precise intervals
- **Multiple Job Types Support**: 
    - `HEARTBEAT`: Simple health check jobs
    - `CONTAINER`: Execute commands in Docker containers
- **Distributed Architecture**: Microservices-based design for scalability
- **High Availability**: Fault-tolerant operation with no single point of failure
- **Observability**: Built-in OpenTelemetry integration for tracing and metrics
- **Security**: JWT-based authentication and authorization

## Architecture

Chronoverse follows a microservices architecture with the following components:

- **users-service**: Handles user authentication and management
- **jobs-service**: Manages job definitions and configurations
- **scheduling-job**: Identifies jobs due for execution and schedules them
- **workflow-job**: Prepares jobs for execution if build step is required
- **execution-job**: Executes scheduled jobs in containers
- **server**: API gateway for client interactions

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