package httpx

import (
	"io"
	"net/http"
	"testing"
	"time"
)

type blockingBody struct {
	closed chan struct{}
}

func (b *blockingBody) Read([]byte) (int, error) {
	<-b.closed
	return 0, io.ErrClosedPipe
}

func (b *blockingBody) Close() error {
	select {
	case <-b.closed:
	default:
		close(b.closed)
	}
	return nil
}

func TestIdleTimeoutBodyClosesStalledRead(t *testing.T) {
	body := &blockingBody{closed: make(chan struct{})}
	wrapped := NewIdleTimeoutBody(body, 20*time.Millisecond)
	result := make(chan error, 1)
	go func() {
		_, err := wrapped.Read(make([]byte, 1))
		result <- err
	}()

	select {
	case err := <-result:
		if err != io.ErrClosedPipe {
			t.Fatalf("Read error = %v, want %v", err, io.ErrClosedPipe)
		}
	case <-time.After(time.Second):
		t.Fatal("stalled read was not interrupted")
	}
}

func TestNewServerStreamingDefaults(t *testing.T) {
	srv := NewServer(http.NotFoundHandler())
	if srv.ReadHeaderTimeout != ReadHeaderTimeout {
		t.Fatalf("ReadHeaderTimeout = %s, want %s", srv.ReadHeaderTimeout, ReadHeaderTimeout)
	}
	if srv.IdleTimeout != IdleTimeout {
		t.Fatalf("IdleTimeout = %s, want %s", srv.IdleTimeout, IdleTimeout)
	}
	if srv.WriteTimeout != 0 {
		t.Fatalf("WriteTimeout = %s, want 0 for streaming responses", srv.WriteTimeout)
	}
}
