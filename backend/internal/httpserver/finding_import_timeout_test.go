package httpserver

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

type findingImportDeadlineWriter struct {
	*httptest.ResponseRecorder
	deadline time.Time
}

func (w *findingImportDeadlineWriter) SetWriteDeadline(deadline time.Time) error {
	w.deadline = deadline
	return nil
}

func TestPrepareFindingImport(t *testing.T) {
	gin.SetMode(gin.TestMode)
	parent, cancelParent := context.WithCancel(context.Background())
	defer cancelParent()
	writer := &findingImportDeadlineWriter{ResponseRecorder: httptest.NewRecorder()}
	c, _ := gin.CreateTestContext(writer)
	c.Request = httptest.NewRequest(http.MethodGet, "/import", nil).WithContext(parent)
	start := time.Now()
	ctx, cancel := prepareFindingImport(c, slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer cancel()
	if writer.deadline.Before(start.Add(findingImportWriteTimeout)) || writer.deadline.After(time.Now().Add(findingImportWriteTimeout)) {
		t.Fatalf("write deadline = %v", writer.deadline)
	}
	deadline, ok := ctx.Deadline()
	if !ok || deadline.Before(start.Add(findingImportTimeout)) || deadline.After(time.Now().Add(findingImportTimeout)) {
		t.Fatalf("execution deadline = %v, has deadline = %v", deadline, ok)
	}
	if !deadline.Before(writer.deadline) {
		t.Fatal("execution must stop before response writes expire")
	}
	cancelParent()
	if ctx.Err() != context.Canceled {
		t.Fatalf("parent cancellation not propagated: %v", ctx.Err())
	}
}

func TestPrepareFindingImportPreservesEarlierDeadline(t *testing.T) {
	gin.SetMode(gin.TestMode)
	parent, cancelParent := context.WithTimeout(context.Background(), time.Second)
	defer cancelParent()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/import", nil).WithContext(parent)
	ctx, cancel := prepareFindingImport(c, slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer cancel()
	want, _ := parent.Deadline()
	got, ok := ctx.Deadline()
	if !ok || !got.Equal(want) {
		t.Fatalf("shorter parent deadline lost: got %v, want %v", got, want)
	}
	cancel()
	if ctx.Err() != context.Canceled {
		t.Fatalf("explicit cancellation not propagated: %v", ctx.Err())
	}
}

func TestFindingImportOverridesServerWriteDeadline(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/import", func(c *gin.Context) {
		_, cancel := prepareFindingImport(c, slog.New(slog.NewTextHandler(io.Discard, nil)))
		defer cancel()
		// Cross a shortened server-wide deadline without a 30s test sleep.
		time.Sleep(30 * time.Millisecond)
		c.JSON(http.StatusOK, gin.H{"completed": true})
	})
	server := httptest.NewUnstartedServer(router)
	server.Config.WriteTimeout = time.Millisecond
	server.Start()
	defer server.Close()
	client := server.Client()
	client.Timeout = 2 * time.Second
	response, err := client.Post(server.URL+"/import", "text/csv", nil)
	if err != nil {
		t.Fatalf("import response lost to global write deadline: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("import status = %d", response.StatusCode)
	}
}
