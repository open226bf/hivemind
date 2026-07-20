package handler

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/open226bf/hivemind/internal/ports"
)

const (
	logStreamHeartbeat = 15 * time.Second
	logStreamLineMax   = 1024 * 1024
)

// parseLogOptions reads the log query parameters shared by the managed-service
// and discovered-service log endpoints.
func parseLogOptions(c *gin.Context) ports.LogOptions {
	return ports.LogOptions{
		Follow:     c.DefaultQuery("follow", "true") == "true",
		Tail:       c.DefaultQuery("tail", "200"),
		Timestamps: c.Query("timestamps") == "true",
		Since:      c.Query("since"),
	}
}

// streamLogs pipes an orchestrator log stream to the client as Server-Sent
// Events: one `data:` frame per line, an `event: end` frame once the upstream
// closes, and a heartbeat comment during lulls.
//
// SSE is long-lived, so the connection's write deadline is cleared — the server
// sets a 30s WriteTimeout that would otherwise sever a quiet follow stream —
// and scanning runs in a goroutine so the writer can still emit heartbeats while
// no line arrives. The caller owns the stream and must Close it.
func streamLogs(c *gin.Context, stream io.ReadCloser) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no") // disable proxy buffering (nginx)
	c.Writer.WriteHeader(http.StatusOK)

	rc := http.NewResponseController(c.Writer)
	_ = rc.SetWriteDeadline(time.Time{})
	_ = rc.Flush()

	ctx := c.Request.Context()
	// Close the upstream stream when the client disconnects so the scanner unblocks.
	go func() {
		<-ctx.Done()
		_ = stream.Close()
	}()

	lines := make(chan string, 64)
	go func() {
		defer close(lines)
		scanner := bufio.NewScanner(stream)
		scanner.Buffer(make([]byte, 0, 64*1024), logStreamLineMax)
		for scanner.Scan() {
			select {
			case lines <- scanner.Text():
			case <-ctx.Done():
				return
			}
		}
	}()

	heartbeat := time.NewTicker(logStreamHeartbeat)
	defer heartbeat.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case line, ok := <-lines:
			if !ok {
				// Upstream ended (non-follow, or the service's log stream closed).
				_, _ = fmt.Fprint(c.Writer, "event: end\ndata: \n\n")
				_ = rc.Flush()
				return
			}
			if _, err := fmt.Fprintf(c.Writer, "data: %s\n\n", line); err != nil {
				return // client gone
			}
			_ = rc.Flush()
		case <-heartbeat.C:
			if _, err := fmt.Fprint(c.Writer, ": ping\n\n"); err != nil {
				return // client gone
			}
			_ = rc.Flush()
		}
	}
}
