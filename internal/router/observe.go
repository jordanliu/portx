package router

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"time"
)

// RequestEvent describes one completed HTTP request handled by PortX.
type RequestEvent struct {
	Timestamp time.Time     `json:"timestamp"`
	RouteID   string        `json:"route_id,omitempty"`
	Host      string        `json:"host"`
	Method    string        `json:"method"`
	Path      string        `json:"path"`
	Status    int           `json:"status"`
	Duration  time.Duration `json:"duration"`
	Bytes     int64         `json:"bytes"`
}

// RequestObserver receives live request events. Observers must not block.
type RequestObserver func(RequestEvent)

// ObserveHandler wraps an HTTP handler and emits one event for each request.
func ObserveHandler(handler http.Handler, observer RequestObserver) http.Handler {
	if observer == nil {
		return handler
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		captured := &responseCapture{ResponseWriter: w}
		defer func() {
			observer(RequestEvent{
				Timestamp: started,
				RouteID:   observedRouteID(r),
				Host:      r.Host,
				Method:    r.Method,
				Path:      requestPath(r),
				Status:    captured.statusCode(),
				Duration:  time.Since(started),
				Bytes:     captured.bytes,
			})
		}()
		handler.ServeHTTP(captured, r)
	})
}

func requestPath(r *http.Request) string {
	if r.URL == nil || r.URL.Path == "" {
		return "/"
	}
	return r.URL.Path
}

type responseCapture struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (w *responseCapture) WriteHeader(status int) {
	informational := status >= 100 && status < 200 && status != http.StatusSwitchingProtocols
	if informational {
		w.ResponseWriter.WriteHeader(status)
		return
	}
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

type observedRouteKey struct{}

func setObservedRouteID(r *http.Request, routeID string) {
	*r = *r.WithContext(context.WithValue(r.Context(), observedRouteKey{}, routeID))
}

func observedRouteID(r *http.Request) string {
	routeID, _ := r.Context().Value(observedRouteKey{}).(string)
	return routeID
}

func (w *responseCapture) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(data)
	w.bytes += int64(n)
	return n, err
}

func (w *responseCapture) ReadFrom(src io.Reader) (int64, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	if reader, ok := w.ResponseWriter.(io.ReaderFrom); ok {
		n, err := reader.ReadFrom(src)
		w.bytes += n
		return n, err
	}
	n, err := io.Copy(w.ResponseWriter, src)
	w.bytes += n
	return n, err
}

func (w *responseCapture) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func (w *responseCapture) Flush() {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *responseCapture) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, http.ErrNotSupported
	}
	if w.status == 0 {
		w.status = http.StatusSwitchingProtocols
	}
	return hijacker.Hijack()
}

func (w *responseCapture) Push(target string, opts *http.PushOptions) error {
	pusher, ok := w.ResponseWriter.(http.Pusher)
	if !ok {
		return http.ErrNotSupported
	}
	return pusher.Push(target, opts)
}

func (w *responseCapture) statusCode() int {
	if w.status == 0 {
		return http.StatusOK
	}
	return w.status
}
