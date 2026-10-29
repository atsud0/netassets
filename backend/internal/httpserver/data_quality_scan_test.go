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

type scanDeadlineWriter struct {
	*httptest.ResponseRecorder
	deadline time.Time
}

func (w *scanDeadlineWriter) SetWriteDeadline(deadline time.Time) error {
	w.deadline = deadline
	return nil
}

func TestPrepareDataQualityScan(t *testing.T) {
	gin.SetMode(gin.TestMode)
	parent, cancelParent := context.WithCancel(context.Background())
	defer cancelParent()
	writer := &scanDeadlineWriter{ResponseRecorder: httptest.NewRecorder()}
	c, _ := gin.CreateTestContext(writer)
	c.Request = httptest.NewRequest(http.MethodGet, "/scan", nil).WithContext(parent)
	start := time.Now()
	ctx, cancel := prepareDataQualityScan(c, slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer cancel()
	if writer.deadline.Before(start.Add(dataQualityScanWriteTimeout)) || writer.deadline.After(time.Now().Add(dataQualityScanWriteTimeout)) {
		t.Fatalf("write deadline = %v", writer.deadline)
	}
	deadline, ok := ctx.Deadline()
	if !ok || deadline.Before(start.Add(dataQualityScanTimeout)) || deadline.After(time.Now().Add(dataQualityScanTimeout)) {
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

func TestPrepareDataQualityScanPreservesEarlierDeadline(t *testing.T) {
	gin.SetMode(gin.TestMode)
	parent, cancelParent := context.WithTimeout(context.Background(), time.Second)
	defer cancelParent()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/scan", nil).WithContext(parent)
	ctx, cancel := prepareDataQualityScan(c, slog.New(slog.NewTextHandler(io.Discard, nil)))
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

func TestDataQualityScanOverridesServerWriteDeadline(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/scan", func(c *gin.Context) {
		_, cancel := prepareDataQualityScan(c, slog.New(slog.NewTextHandler(io.Discard, nil)))
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
	response, err := client.Get(server.URL + "/scan")
	if err != nil {
		t.Fatalf("scan response lost to global write deadline: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("scan status = %d", response.StatusCode)
	}
}
