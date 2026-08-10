//go:build idempotency_stack

package protocol_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/cookiejar"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const idempotencyHeader = "Idempotency-Key"

func TestDevelopmentStackIdempotency(t *testing.T) {
	baseURL := envOr("IDEMPOTENCY_STACK_HTTP_URL", "http://localhost:8080")
	for _, port := range []string{"50051", "50052", "50053", "50054"} {
		connection, err := net.DialTimeout("tcp", net.JoinHostPort("localhost", port), 2*time.Second)
		if err != nil {
			t.Fatalf("gRPC endpoint %s is unavailable: %v", port, err)
		}
		_ = connection.Close()
	}

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar, Timeout: 10 * time.Second}
	email := fmt.Sprintf("idempotency-%d@example.com", time.Now().UnixNano())
	registerKey := fmt.Sprintf("register-%d", time.Now().UnixNano())
	registration := []byte(fmt.Sprintf(`{"email":%q,"password":"stack-password"}`, email))

	first := postJSON(t, client, baseURL+"/auth/register", registerKey, registration)
	if first.StatusCode != http.StatusCreated {
		t.Fatalf("fresh registration status = %d, want 201", first.StatusCode)
	}
	firstCookies := first.Header.Values("Set-Cookie")
	_ = first.Body.Close()
	second := postJSON(t, client, baseURL+"/auth/register", registerKey, registration)
	if second.StatusCode != http.StatusCreated {
		t.Fatalf("registration replay status = %d, want 201", second.StatusCode)
	}
	if fmt.Sprint(firstCookies) == fmt.Sprint(second.Header.Values("Set-Cookie")) {
		t.Fatal("registration replay did not issue fresh authentication material")
	}
	_ = second.Body.Close()
	changed := postJSON(t, client, baseURL+"/auth/register", registerKey, []byte(fmt.Sprintf(`{"email":%q,"password":"changed-password"}`, email)))
	if changed.StatusCode != http.StatusConflict {
		t.Fatalf("changed registration replay status = %d, want 409", changed.StatusCode)
	}
	_ = changed.Body.Close()

	workflowKey := fmt.Sprintf("workflow-%d", time.Now().UnixNano())
	workflowBody := []byte(`{"name":"idempotency-stack","payload":"{\"endpoint\":\"http://server:8080/health\",\"expected_status_code\":200,\"headers\":{}}","kind":"HEARTBEAT","interval":60,"max_consecutive_job_failures_allowed":3}`)
	const callers = 8
	responses := make(chan workflowCreateResult, callers)
	var wg sync.WaitGroup
	for range callers {
		wg.Go(func() {
			responses <- createWorkflow(client, baseURL, workflowKey, workflowBody)
		})
	}
	wg.Wait()
	close(responses)
	var workflowID string
	for response := range responses {
		if response.err != nil {
			t.Fatal(response.err)
		}
		if response.status != http.StatusCreated {
			t.Fatalf("concurrent workflow status = %d, want 201", response.status)
		}
		if workflowID == "" {
			workflowID = response.id
		} else if response.id != workflowID {
			t.Fatalf("same-key calls created different workflows: %s and %s", workflowID, response.id)
		}
	}

	differentBody := bytes.Replace(workflowBody, []byte("idempotency-stack"), []byte("idempotency-conflict"), 1)
	conflict := postJSON(t, client, baseURL+"/workflows", workflowKey, differentBody)
	if conflict.StatusCode != http.StatusConflict {
		t.Fatalf("same-key/different-workflow status = %d, want 409", conflict.StatusCode)
	}
	_ = conflict.Body.Close()
	distinct := createWorkflow(client, baseURL, workflowKey+"-distinct", workflowBody)
	if distinct.err != nil || distinct.status != http.StatusCreated || distinct.id == workflowID {
		t.Fatalf("new-key/same-body result = %+v, original = %s", distinct, workflowID)
	}

	assertDatabaseContract(t, email, registerKey, workflowKey)
}

type workflowCreateResult struct {
	id     string
	status int
	err    error
}

func createWorkflow(client *http.Client, baseURL, key string, body []byte) workflowCreateResult {
	request, err := http.NewRequest(http.MethodPost, baseURL+"/workflows", bytes.NewReader(body))
	if err != nil {
		return workflowCreateResult{err: err}
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(idempotencyHeader, key)
	response, err := client.Do(request)
	if err != nil {
		return workflowCreateResult{err: err}
	}
	defer response.Body.Close()
	var payload struct {
		ID string `json:"id"`
	}
	if response.StatusCode == http.StatusCreated {
		err = json.NewDecoder(response.Body).Decode(&payload)
	}
	return workflowCreateResult{id: payload.ID, status: response.StatusCode, err: err}
}

func postJSON(t *testing.T, client *http.Client, url, key string, body []byte) *http.Response {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(idempotencyHeader, key)
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func assertDatabaseContract(t *testing.T, email, registerKey, workflowKey string) {
	t.Helper()
	pool, err := pgxpool.New(t.Context(), envOr("IDEMPOTENCY_STACK_POSTGRES_URL", "postgres://primary:chronoverse@localhost:5432/chronoverse?sslmode=disable"))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	var timestampColumns int
	err = pool.QueryRow(t.Context(), `
		SELECT count(*)
		FROM information_schema.columns
		WHERE table_schema = 'public'
		  AND table_name IN ('command_idempotency_keys', 'workflow_terminal_effects')
		  AND column_name IN ('created_at', 'updated_at', 'completed_at', 'expires_at')
		  AND data_type = 'timestamp without time zone'
	`).Scan(&timestampColumns)
	if err != nil || timestampColumns != 5 {
		t.Fatalf("timestamp contract count = %d, want 5: %v", timestampColumns, err)
	}

	var workflowRows int
	err = pool.QueryRow(t.Context(), `
		SELECT count(*)
		FROM command_idempotency_keys AS ledger
		JOIN users AS u ON ledger.scope = 'user:' || u.id::text
		WHERE u.email = $1 AND ledger.operation = 'workflow.create' AND ledger.idempotency_key = $2
	`, email, workflowKey).Scan(&workflowRows)
	if err != nil || workflowRows != 1 {
		t.Fatalf("workflow ledger rows = %d, want 1: %v", workflowRows, err)
	}

	cleanupCtx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	_, _ = pool.Exec(cleanupCtx, `DELETE FROM command_idempotency_keys WHERE (scope = 'public' AND operation = 'user.register' AND idempotency_key = $2) OR scope IN (SELECT 'user:' || id::text FROM users WHERE email = $1)`, email, registerKey)
	_, _ = pool.Exec(cleanupCtx, `DELETE FROM users WHERE email = $1`, email)
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
