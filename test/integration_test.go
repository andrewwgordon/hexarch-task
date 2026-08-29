// integration_test.go is the end-to-end suite (external test package). It
// boots the real httpapi server over a real TCP socket on a free port,
// backed by a SQLite file created through the repository factory, and drives
// the complete task lifecycle over HTTP to prove the full wiring works.
package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"hexarch/internal/adapters/httpapi"
	"hexarch/internal/application"
	"hexarch/internal/repository"

	// Registers the sqlite backend with the factory, exercising the same
	// wiring as cmd/main.go.
	_ "hexarch/internal/repository/sqlite"
)

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("freePort listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return port
}

func startTestServer(t *testing.T) (baseURL string, stop func()) {
	t.Helper()

	tmp := t.TempDir() + "/tasks.db"
	repo, closer, err := repository.New(context.Background(),
		repository.Config{Type: repository.TypeSQLite, URI: tmp})
	if err != nil {
		t.Fatalf("repository.New: %v", err)
	}
	svc := application.NewTaskService(repo)

	port := freePort(t)
	api := httpapi.NewApi(svc)

	done := make(chan error, 1)
	go func() {
		addrArg := fmt.Sprintf(":%d", port)
		done <- api.Run(context.Background(), []string{addrArg})
	}()

	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	deadline := time.Now().Add(5 * time.Second)
	for {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 200*time.Millisecond)
		if err == nil {
			conn.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("server did not start: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}

	stop = func() {
		_ = done
		_ = closer.Close()
	}
	return base, stop
}

func doJSON(method, url, body string) (int, []byte, error) {
	var rd io.Reader
	if body != "" {
		rd = bytes.NewBufferString(body)
	}
	req, err := http.NewRequest(method, url, rd)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	return resp.StatusCode, data, err
}

func TestIntegrationFullParity(t *testing.T) {
	base, stop := startTestServer(t)
	defer stop()

	code, body, err := doJSON("POST", base+"/api/tasks", `{"title":"Ship v1","priority":5}`)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if code != http.StatusCreated {
		t.Fatalf("create status = %d, body=%s", code, body)
	}
	var created struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatalf("create decode: %v", err)
	}
	if created.Status != "todo" || created.ID == "" {
		t.Fatalf("create = %+v", created)
	}
	id := created.ID

	if code, body, _ := doJSON("GET", base+"/api/tasks", ""); code != http.StatusOK {
		t.Fatalf("list status = %d, body=%s", code, body)
	}

	if code, body, _ := doJSON("GET", base+"/api/tasks/"+id, ""); code != http.StatusOK {
		t.Fatalf("get status = %d, body=%s", code, body)
	}

	if code, body, _ := doJSON("PATCH", base+"/api/tasks/"+id, `{"title":"Ship v1 (revised)"}`); code != http.StatusOK {
		t.Fatalf("rename status = %d, body=%s", code, body)
	}

	// Editable-field patches (priority, deadline) must happen before the task
	// reaches the terminal "done" state: the domain rejects edits to done
	// tasks, so the priority/deadline steps run while the task is
	// in_progress.
	if code, body, _ := doJSON("PATCH", base+"/api/tasks/"+id, `{"priority":4}`); code != http.StatusOK {
		t.Fatalf("priority status = %d, body=%s", code, body)
	}

	if code, body, _ := doJSON("PATCH", base+"/api/tasks/"+id, `{"deadline":"2026-10-15"}`); code != http.StatusOK {
		t.Fatalf("deadline status = %d, body=%s", code, body)
	}

	if code, body, _ := doJSON("PATCH", base+"/api/tasks/"+id, `{"status":"in_progress"}`); code != http.StatusOK {
		t.Fatalf("start status = %d, body=%s", code, body)
	}

	if code, body, _ := doJSON("PATCH", base+"/api/tasks/"+id, `{"status":"done"}`); code != http.StatusOK {
		t.Fatalf("done status = %d, body=%s", code, body)
	}

	if code, body, _ := doJSON("GET", base+"/api/stats", ""); code != http.StatusOK {
		t.Fatalf("stats status = %d, body=%s", code, body)
	}

	if code, _, _ := doJSON("DELETE", base+"/api/tasks/"+id, ""); code != http.StatusNoContent {
		t.Fatalf("delete status = %d", code)
	}
	if code, _, _ := doJSON("GET", base+"/api/tasks/"+id, ""); code != http.StatusNotFound {
		t.Fatalf("get after delete status = %d, want 404", code)
	}
}
