package httpx

import (
	"io"
	"net/http"
	"time"
)

const (
	ReadHeaderTimeout     = 30 * time.Second
	RequestBodyIdleLimit  = 5 * time.Minute
	ResponseBodyIdleLimit = 5 * time.Minute
	IdleTimeout           = 90 * time.Second
	MaxHeaderBytes        = 32 << 10
)

// IdleTimeoutBody limits the time spent waiting for one read to make progress.
// It preserves long-lived streams that periodically send data while stopping
// stalled request bodies and response bodies from holding a connection forever.
type IdleTimeoutBody struct {
	body    io.ReadCloser
	timeout time.Duration
}

func NewIdleTimeoutBody(body io.ReadCloser, timeout time.Duration) *IdleTimeoutBody {
	return &IdleTimeoutBody{
		body:    body,
		timeout: timeout,
	}
}

func (b *IdleTimeoutBody) Read(p []byte) (int, error) {
	done := make(chan struct{})
	timer := time.AfterFunc(b.timeout, func() {
		select {
		case <-done:
		default:
			_ = b.body.Close()
		}
	})
	n, err := b.body.Read(p)
	close(done)
	timer.Stop()
	return n, err
}

func (b *IdleTimeoutBody) Close() error {
	return b.body.Close()
}

func (b *IdleTimeoutBody) Write(p []byte) (int, error) {
	writer, ok := b.body.(io.Writer)
	if !ok {
		return 0, io.ErrClosedPipe
	}
	return writer.Write(p)
}

func NewServer(handler http.Handler) *http.Server {
	return &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: ReadHeaderTimeout,
		MaxHeaderBytes:    MaxHeaderBytes,
		IdleTimeout:       IdleTimeout,
		// Keep WriteTimeout disabled so SSE and WebSocket responses can remain
		// open as long as they continue to make progress.
	}
}
