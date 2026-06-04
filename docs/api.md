# HTTP API
 
The HTTP API is served by `server`. Development exposes it directly on
`http://localhost:8080`. Production exposes it through Nginx under `/api/...`;
for example, development `GET /workflows` maps to production
`GET /api/workflows`.
 
All non-auth application routes require a valid session cookie. Mutating routes
also require CSRF validation. Browser clients get the session and CSRF cookies
from login or registration.
 
## Common Behavior
 
### Headers
 
- `Content-Type: application/json` for JSON request bodies.
- `Idempotency-Key: <unique-key>` is required for retry-prone mutations:
  - `POST /workflows`
  - `PUT /workflows/{workflow_id}`
  - `POST /workflows/{workflow_id}/jobs/schedule`
 
Use a stable idempotency key when retrying the same user action. Use a new key
for a new action.
 
### Status Mapping
 
The server maps gRPC errors to HTTP status codes. Important cases:
 
- `400 Bad Request`: invalid input, invalid filters, missing idempotency key.
- `401 Unauthorized`: missing or invalid session.
- `403 Forbidden`: permission denied.
- `404 Not Found`: resource not found.
- `409 Conflict`: already exists or aborted operation.
- `412 Precondition Failed`: state precondition failed, including disabled log
  retention on retained log read/search routes. SSE stream-open failures are
  sent as `event: error` frames after stream headers are written.
- `429 Too Many Requests`: resource exhausted.
- `503 Service Unavailable`: downstream service unavailable.
 
## Authentication
 
### Register
 
`POST /auth/register`
 
Body:
 
```json
{
  "email": "user@example.com",
  "password": "password"
}
```
 
Creates the user, issues a session cookie, and returns `201 Created`.
 
### Login
 
`POST /auth/login`
 
Body:
 
```json
{
  "email": "user@example.com",
  "password": "password"
}
```
 
Issues a session cookie and returns `201 Created`.
 
### Logout
 
`POST /auth/logout`
 
Deletes the session and CSRF cookies and returns `204 No Content`.
 
### Validate Session
 
`POST /auth/validate`
 
Returns `200 OK` when the current session and CSRF token are valid.
 
## Users
 
### Get Current User
 
`GET /users`
 
Returns the current user's profile and preference data.
 
### Update Current User
 
`PUT /users`
 
Body:
 
```json
{
  "notification_preference": "ALL"
}
```
 
Returns `204 No Content`. The current handler accepts a `password` field in the
shape but only forwards notification preference updates.
 
## Workflows
 
Workflow kinds:
 
- `HEARTBEAT`: no execution logs and `log_retention` must be disabled.
- `CONTAINER`: executes a container workload and may retain logs.
 
Build statuses:
 
- `QUEUED`
- `STARTED`
- `COMPLETED`
- `FAILED`
- `CANCELED`
 
### List Workflows
 
`GET /workflows`
 
Query parameters:
 
- `cursor`: pagination cursor.
- `query`: text query.
- `kind`: `HEARTBEAT` or `CONTAINER`.
- `build_status`: one of the build statuses above.
- `terminated`: boolean string.
- `interval_min`: minimum interval in minutes.
- `interval_max`: maximum interval in minutes.
 
Response includes `workflows` and `cursor`.
 
### Create Workflow
 
`POST /workflows`
 
Requires `Idempotency-Key`.
 
Body:
 
```json
{
  "name": "nightly-report",
  "payload": "{\"image\":\"alpine:latest\",\"cmd\":[\"echo\",\"hello\"]}",
  "kind": "CONTAINER",
  "interval": 60,
  "max_consecutive_job_failures_allowed": 3,
  "log_retention": true
}
```
 
Fields:
 
- `name`: display name.
- `payload`: workflow-kind-specific JSON string.
- `kind`: `HEARTBEAT` or `CONTAINER`.
- `interval`: schedule interval in minutes.
- `max_consecutive_job_failures_allowed`: failure threshold before workflow
  behavior changes.
- `log_retention`: optional. `CONTAINER` defaults to retained logs when unset;
  `HEARTBEAT` persists with retention disabled.
 
Returns `201 Created` with the workflow ID.
 
### Get Workflow
 
`GET /workflows/{workflow_id}`
 
Returns workflow fields including:
 
- `id`
- `name`
- `payload`
- `kind`
- `build_status`
- `interval`
- `consecutive_job_failures_count`
- `max_consecutive_job_failures_allowed`
- `created_at`
- `updated_at`
- `terminated_at`
- `log_retention`
- `generation`
 
### Update Workflow
 
`PUT /workflows/{workflow_id}`
 
Requires `Idempotency-Key`.
 
Body:
 
```json
{
  "name": "nightly-report",
  "payload": "{\"image\":\"alpine:latest\",\"cmd\":[\"echo\",\"updated\"]}",
  "interval": 120,
  "max_consecutive_job_failures_allowed": 3
}
```
 
Returns `204 No Content`.
 
### Terminate Workflow
 
`PATCH /workflows/{workflow_id}`
 
Terminates the workflow and returns `204 No Content`.
 
### Delete Workflow
 
`DELETE /workflows/{workflow_id}`
 
Deletes a terminated workflow and returns `204 No Content`. Active workflows or
workflows with active jobs can fail with `412 Precondition Failed`.
 
## Jobs
 
Job statuses:
 
- `PENDING`
- `QUEUED`
- `RUNNING`
- `COMPLETED`
- `FAILED`
- `CANCELED`
 
Job triggers:
 
- `AUTOMATIC`
- `MANUAL`
 
### List Jobs
 
`GET /workflows/{workflow_id}/jobs`
 
Query parameters:
 
- `cursor`: pagination cursor.
- `status`: job status filter.
- `trigger`: `AUTOMATIC` or `MANUAL`.
 
Response includes `jobs` and `cursor`. Job entries include `attempts` in list
responses.
 
### Manual Schedule
 
`POST /workflows/{workflow_id}/jobs/schedule`
 
Requires `Idempotency-Key`.
 
Schedules an immediate manual job and returns `201 Created` with the job ID.
 
### Get Job
 
`GET /workflows/{workflow_id}/jobs/{job_id}`
 
Returns:
 
- `id`
- `workflow_id`
- `status`
- `trigger`
- `scheduled_at`
- `started_at`
- `completed_at`
- `created_at`
- `updated_at`
 
Lease metadata such as lease tokens is internal to worker gRPC APIs and is not
returned by the public HTTP job detail route.
 
## Job Logs
 
Log streams:
 
- `stdout`
- `stderr`
- omit `stream` for all streams.
 
Log APIs are available only when the workflow supports logs and log retention is
enabled. `HEARTBEAT` workflows do not produce execution logs. If retention is
disabled, retained log read/search routes return `412 Precondition Failed`; live
SSE streams report the failure as an `event: error` frame.
 
### Get Logs
 
`GET /workflows/{workflow_id}/jobs/{job_id}/logs`
 
Query parameters:
 
- `cursor`: pagination cursor.
 
Returns retained logs from ClickHouse in descending sequence order: latest logs
first, older logs on later cursor pages.
 
### Search Logs
 
`GET /workflows/{workflow_id}/jobs/{job_id}/logs/search`
 
Query parameters:
 
- `cursor`: pagination cursor.
- `stream`: `stdout` or `stderr`.
- `q`: message search text.
 
If `q` is omitted, the route returns filtered retained logs by stream. If `q` is
present, it searches retained logs through Meilisearch. Results are returned in
descending sequence order: latest logs first, older logs on later cursor pages.
 
### Download Raw Logs
 
`GET /workflows/{workflow_id}/jobs/{job_id}/logs/raw`
 
Streams retained logs as `text/plain` for terminal jobs. Non-terminal jobs return
`400 Bad Request`. Raw downloads are streamed in ascending sequence order so the
file reads oldest to newest.
 
### Stream Live Logs
 
`GET /workflows/{workflow_id}/jobs/{job_id}/events`
 
Streams Server-Sent Events for a running job. Production Nginx has a dedicated
no-buffering proxy location for this route:
 
`/api/workflows/{workflow_id}/jobs/{job_id}/events`
 
After the SSE headers are sent, stream-open and stream-receive failures are sent
as `event: error` frames on the `text/event-stream` response. For example, a
non-running job or disabled log retention is reported as an SSE error event
rather than an HTTP `412 Precondition Failed` response.
 
## Notifications
 
### List Notifications
 
`GET /notifications`
 
Query parameters:
 
- `cursor`: pagination cursor.
 
Returns notifications for the current user.
 
### Mark Notifications Read
 
`PUT /notifications`
 
Body:
 
```json
{
  "ids": ["notification-id"]
}
```
 
Returns `204 No Content`.
 
## Analytics
 
### User Analytics
 
`GET /analytics`
 
Returns current-user aggregate totals:
 
- `total_workflows`
- `total_jobs`
- `total_joblogs`
- `total_job_execution_duration`
 
### Workflow Analytics
 
`GET /analytics/{workflow_id}`
 
Returns workflow aggregate totals:
 
- `workflow_id`
- `total_job_execution_duration`
- `total_jobs`
- `total_joblogs`
