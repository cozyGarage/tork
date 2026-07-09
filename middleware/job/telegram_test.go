package job

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/runabol/tork"
	"github.com/runabol/tork/datastore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stubLogDS struct {
	parts map[string][]*tork.TaskLogPart
	jobs  map[string]*tork.Job
}

func (s *stubLogDS) GetJobByID(_ context.Context, id string) (*tork.Job, error) {
	if j, ok := s.jobs[id]; ok {
		return j, nil
	}
	return nil, datastore.ErrJobNotFound
}

func (s *stubLogDS) GetTaskLogParts(_ context.Context, taskID, _ string, page, size int) (*datastore.Page[*tork.TaskLogPart], error) {
	items := s.parts[taskID]
	if len(items) > size {
		items = items[:size]
	}
	return &datastore.Page[*tork.TaskLogPart]{
		Items:      items,
		Number:     page,
		Size:       len(items),
		TotalPages: 1,
		TotalItems: len(items),
	}, nil
}

func testTelegramMW(t *testing.T, cfg TelegramConfig, j *tork.Job) int32 {
	t.Helper()
	var posts atomic.Int32
	svr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		var payload map[string]string
		require.NoError(t, json.Unmarshal(body, &payload))
		assert.Contains(t, payload["text"], j.Name)
		posts.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(svr.Close)

	oldBase := telegramAPIBase
	telegramAPIBase = svr.URL + "/bot%s/sendMessage"
	t.Cleanup(func() { telegramAPIBase = oldBase })

	hm := ApplyMiddleware(NoOpHandlerFunc, []MiddlewareFunc{Telegram(&stubLogDS{jobs: map[string]*tork.Job{j.ID: j}}, cfg)})
	require.NoError(t, hm(context.Background(), StateChange, j))
	time.Sleep(100 * time.Millisecond)
	return posts.Load()
}

func TestTelegramJobFailed(t *testing.T) {
	cfg := TelegramConfig{
		Enabled:  true,
		Token:    "tok",
		ChatID:   "123",
		OnStates: []string{"FAILED"},
		LogLines: 10,
	}
	j := &tork.Job{
		ID:    "job-1",
		Name:  "agenda-fail-test",
		State: tork.JobStateFailed,
		Execution: []*tork.Task{{
			ID:    "task-1",
			Name:  "send agenda",
			State: tork.TaskStateFailed,
			Error: "exit 1",
		}},
	}
	assert.Equal(t, int32(1), testTelegramMW(t, cfg, j))
}

func TestTelegramJobCompleted(t *testing.T) {
	cfg := TelegramConfig{Enabled: true, Token: "tok", ChatID: "123", OnStates: []string{"FAILED"}}
	j := &tork.Job{
		ID:    "job-1",
		Name:  "ok-job",
		State: tork.JobStateCompleted,
	}
	assert.Equal(t, int32(0), testTelegramMW(t, cfg, j))
}

func TestTelegramDisabled(t *testing.T) {
	cfg := TelegramConfig{Enabled: false, Token: "tok", ChatID: "123", OnStates: []string{"FAILED"}}
	j := &tork.Job{
		ID:    "job-1",
		Name:  "fail-job",
		State: tork.JobStateFailed,
		Execution: []*tork.Task{{
			Name:  "t",
			State: tork.TaskStateFailed,
		}},
	}
	assert.Equal(t, int32(0), testTelegramMW(t, cfg, j))
}

func TestTelegramOnStatesExcludesFailed(t *testing.T) {
	cfg := TelegramConfig{Enabled: true, Token: "tok", ChatID: "123", OnStates: []string{"COMPLETED"}}
	j := &tork.Job{
		ID:    "job-1",
		Name:  "fail-job",
		State: tork.JobStateFailed,
		Execution: []*tork.Task{{
			Name:  "t",
			State: tork.TaskStateFailed,
		}},
	}
	assert.Equal(t, int32(0), testTelegramMW(t, cfg, j))
}

func TestTelegramReloadsExecution(t *testing.T) {
	var body string
	svr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		w.WriteHeader(http.StatusOK)
	}))
	defer svr.Close()

	oldBase := telegramAPIBase
	telegramAPIBase = svr.URL + "/bot%s/sendMessage"
	defer func() { telegramAPIBase = oldBase }()

	// Stale job like errorHandler passes: FAILED state but empty Execution.
	stale := &tork.Job{
		ID: "job-stale", Name: "stale-job", State: tork.JobStateFailed, Error: "exit 1",
	}
	full := &tork.Job{
		ID: "job-stale", Name: "stale-job", State: tork.JobStateFailed,
		Execution: []*tork.Task{{
			ID: "task-1", Name: "real task", State: tork.TaskStateFailed, Error: "exit 1",
		}},
	}
	ds := &stubLogDS{jobs: map[string]*tork.Job{"job-stale": full}}
	cfg := TelegramConfig{Enabled: true, Token: "tok", ChatID: "123", OnStates: []string{"FAILED"}}
	hm := ApplyMiddleware(NoOpHandlerFunc, []MiddlewareFunc{Telegram(ds, cfg)})
	require.NoError(t, hm(context.Background(), StateChange, stale))
	time.Sleep(100 * time.Millisecond)
	assert.Contains(t, body, "real task")
}

func TestTelegramIncludesTaskNameAndLogTail(t *testing.T) {
	var body string
	svr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		w.WriteHeader(http.StatusOK)
	}))
	defer svr.Close()

	oldBase := telegramAPIBase
	telegramAPIBase = svr.URL + "/bot%s/sendMessage"
	defer func() { telegramAPIBase = oldBase }()

	j := &tork.Job{
		ID:    "job-1",
		Name:  "my-job",
		State: tork.JobStateFailed,
		Execution: []*tork.Task{{
			ID:    "task-1",
			Name:  "run helper",
			State: tork.TaskStateFailed,
			Error: "boom",
		}},
	}
	ds := &stubLogDS{
		parts: map[string][]*tork.TaskLogPart{"task-1": {{Contents: "line one\nline two\n"}}},
		jobs:  map[string]*tork.Job{"job-1": j},
	}
	cfg := TelegramConfig{Enabled: true, Token: "tok", ChatID: "123", OnStates: []string{"FAILED"}, LogLines: 5}
	hm := ApplyMiddleware(NoOpHandlerFunc, []MiddlewareFunc{Telegram(ds, cfg)})
	require.NoError(t, hm(context.Background(), StateChange, j))
	time.Sleep(100 * time.Millisecond)

	assert.Contains(t, body, "run helper")
	assert.Contains(t, body, "line two")
}

func TestEscHTML(t *testing.T) {
	assert.Equal(t, `a &amp; b &lt;c&gt;`, escHTML(`a & b <c>`))
}

func TestLogTailEmpty(t *testing.T) {
	assert.Equal(t, "", logTail(context.Background(), &stubLogDS{}, "", 10))
}
