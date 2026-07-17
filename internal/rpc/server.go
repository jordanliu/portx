package rpc

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"sync"
	"time"

	"portx/internal/apperr"
	"portx/internal/credentials"
	"portx/internal/leases"
	"portx/internal/origin"
)

const (
	maxRPCRequestBytes = 1 << 20
	rpcReadTimeout     = 60 * time.Second
	rpcWriteTimeout    = 10 * time.Second
)

type Handler interface {
	GetStatus() (StatusResult, error)
	AcquireLease(p AcquireParams) (leases.Lease, error)
	RenewLease(id, token string) (leases.Lease, error)
	ReleaseLease(id, token string) error
	ForceRelease(id string) error
	ListLeases() ([]leases.Lease, error)
	StartTunnel() error
	StopTunnel() error
}

type ContextHandler interface {
	AcquireLeaseContext(context.Context, AcquireParams) (leases.Lease, error)
	StartTunnelContext(context.Context) error
	StopTunnelContext(context.Context) error
}

type Server struct {
	handler  Handler
	ln       net.Listener
	auth     string
	authPath string
	mu       sync.Mutex
	handlers sync.WaitGroup
	close    sync.Once
	ctx      context.Context
	cancel   context.CancelFunc
}

func NewServer(h Handler) *Server {
	ctx, cancel := context.WithCancel(context.Background())
	return &Server{handler: h, ctx: ctx, cancel: cancel}
}

func (s *Server) Serve(sockPath string) error {
	defer s.Close()
	_ = os.Remove(sockPath)
	auth, err := createAuthToken(authPath(sockPath))
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(authPath(sockPath)) }()
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		return err
	}
	if err := os.Chmod(sockPath, 0o600); err != nil {
		_ = ln.Close()
		return err
	}
	s.mu.Lock()
	s.ln = ln
	s.auth = auth
	s.authPath = authPath(sockPath)
	s.mu.Unlock()
	for {
		conn, err := ln.Accept()
		if err != nil {
			if s.isClosed() {
				return nil
			}
			return err
		}
		s.handlers.Add(1)
		go s.handle(conn)
	}
}

func (s *Server) Close() error {
	var closeErr error
	s.close.Do(func() {
		s.cancel()
		s.mu.Lock()
		ln := s.ln
		authPath := s.authPath
		s.mu.Unlock()
		if ln != nil {
			closeErr = ln.Close()
		}
		if authPath != "" {
			if err := os.Remove(authPath); err != nil && !os.IsNotExist(err) {
				closeErr = errors.Join(closeErr, err)
			}
		}
	})
	s.handlers.Wait()
	return closeErr
}

func (s *Server) handle(conn net.Conn) {
	defer s.handlers.Done()
	defer conn.Close()
	reader := bufio.NewReaderSize(conn, 64<<10)
	enc := json.NewEncoder(conn)
	for {
		if err := conn.SetReadDeadline(time.Now().Add(rpcReadTimeout)); err != nil {
			return
		}
		req, err := readRequest(reader)
		if err != nil {
			return
		}
		if subtle.ConstantTimeCompare([]byte(req.Auth), []byte(s.auth)) != 1 {
			return
		}
		select {
		case <-s.ctx.Done():
			return
		default:
		}
		resp := s.dispatchContext(s.ctx, req)
		if err := conn.SetWriteDeadline(time.Now().Add(rpcWriteTimeout)); err != nil {
			return
		}
		if err := enc.Encode(resp); err != nil {
			return
		}
	}
}

func (s *Server) isClosed() bool {
	select {
	case <-s.ctx.Done():
		return true
	default:
		return false
	}
}

func readRequest(reader *bufio.Reader) (Request, error) {
	payload := []byte{}
	for {
		fragment, err := reader.ReadSlice('\n')
		if len(payload)+len(fragment) > maxRPCRequestBytes {
			return Request{}, fmt.Errorf("rpc request exceeds %d bytes", maxRPCRequestBytes)
		}
		payload = append(payload, fragment...)
		if err == nil {
			break
		}
		if err != bufio.ErrBufferFull {
			return Request{}, err
		}
	}
	var req Request
	dec := json.NewDecoder(bytes.NewReader(payload))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		return Request{}, err
	}
	return req, nil
}

func createAuthToken(path string) (string, error) {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return "", err
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := fmt.Sprintf("%x", raw)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", err
	}
	if _, err := f.WriteString(token + "\n"); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return "", err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	if err := credentials.SecurePrivatePath(path, 0o600); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("secure daemon authentication token: %w", err)
	}
	return token, nil
}

func (s *Server) dispatch(req Request) Response {
	return s.dispatchContext(context.Background(), req)
}

func (s *Server) dispatchContext(ctx context.Context, req Request) Response {
	if req.Version != Version {
		return Response{OK: false, Code: apperr.ExitDaemon, Error: "unsupported protocol version"}
	}
	switch req.Method {
	case "GetStatus":
		st, err := s.handler.GetStatus()
		if err != nil {
			return errResp(err)
		}
		b, err := json.Marshal(st)
		if err != nil {
			return errResp(err)
		}
		m := map[string]any{}
		if err := json.Unmarshal(b, &m); err != nil {
			return errResp(err)
		}
		return Response{OK: true, Result: m}
	case "AcquireLease":
		var p AcquireParams
		if err := mapDecode(req.Params, &p); err != nil {
			return Response{OK: false, Code: apperr.ExitInvalidArgs, Error: err.Error()}
		}
		if err := validateAcquireParams(&p); err != nil {
			return Response{OK: false, Code: apperr.ExitInvalidArgs, Error: err.Error()}
		}
		var l leases.Lease
		var err error
		if h, ok := s.handler.(ContextHandler); ok {
			l, err = h.AcquireLeaseContext(ctx, p)
		} else {
			l, err = s.handler.AcquireLease(p)
		}
		if err != nil {
			return errResp(err)
		}
		return Response{OK: true, Result: leaseToMapPrivate(l)}
	case "RenewLease":
		id, _ := req.Params["id"].(string)
		token, _ := req.Params["owner_token"].(string)
		l, err := s.handler.RenewLease(id, token)
		if err != nil {
			return errResp(err)
		}
		return Response{OK: true, Result: leaseToMapPrivate(l)}
	case "ReleaseLease":
		id, _ := req.Params["id"].(string)
		token, _ := req.Params["owner_token"].(string)
		if err := s.handler.ReleaseLease(id, token); err != nil {
			return errResp(err)
		}
		return Response{OK: true}
	case "ForceRelease":
		id, _ := req.Params["id"].(string)
		if err := s.handler.ForceRelease(id); err != nil {
			return errResp(err)
		}
		return Response{OK: true}
	case "ListLeases":
		list, err := s.handler.ListLeases()
		if err != nil {
			return errResp(err)
		}
		arr := make([]any, 0, len(list))
		for _, l := range list {
			arr = append(arr, leaseToMap(l)) // no owner_token
		}
		return Response{OK: true, Result: map[string]any{"leases": arr}}
	case "StartTunnel":
		var err error
		if h, ok := s.handler.(ContextHandler); ok {
			err = h.StartTunnelContext(ctx)
		} else {
			err = s.handler.StartTunnel()
		}
		if err != nil {
			return errResp(err)
		}
		return Response{OK: true}
	case "StopTunnel":
		var err error
		if h, ok := s.handler.(ContextHandler); ok {
			err = h.StopTunnelContext(ctx)
		} else {
			err = s.handler.StopTunnel()
		}
		if err != nil {
			return errResp(err)
		}
		return Response{OK: true}
	default:
		return Response{OK: false, Code: apperr.ExitDaemon, Error: "unknown method"}
	}
}

func validateAcquireParams(params *AcquireParams) error {
	if err := origin.ValidateDNSHostname(params.Hostname); err != nil {
		return err
	}
	if params.PathPrefix == "" {
		params.PathPrefix = "/"
	}
	if err := origin.ValidatePathPrefix(params.PathPrefix); err != nil {
		return err
	}
	if params.HostHeader != "" {
		if err := origin.ValidateHostHeader(params.HostHeader); err != nil {
			return err
		}
	}
	return nil
}

func errResp(err error) Response {
	code := apperr.ExitCode(err)
	if code == 1 {
		code = apperr.ExitDaemon
	}
	return Response{OK: false, Code: code, Error: err.Error()}
}

func mapDecode(m map[string]any, v any) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}
