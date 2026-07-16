package rpc

import (
	"encoding/json"
	"net"
	"os"
	"sync"

	"portx/internal/apperr"
	"portx/internal/leases"
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

type Server struct {
	handler Handler
	ln      net.Listener
	mu      sync.Mutex
}

func NewServer(h Handler) *Server {
	return &Server{handler: h}
}

func (s *Server) Serve(sockPath string) error {
	_ = os.Remove(sockPath)
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
	s.mu.Unlock()
	for {
		conn, err := ln.Accept()
		if err != nil {
			return err
		}
		go s.handle(conn)
	}
}

func (s *Server) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ln != nil {
		return s.ln.Close()
	}
	return nil
}

func (s *Server) handle(conn net.Conn) {
	defer conn.Close()
	dec := json.NewDecoder(conn)
	enc := json.NewEncoder(conn)
	for {
		var req Request
		if err := dec.Decode(&req); err != nil {
			return
		}
		resp := s.dispatch(req)
		if err := enc.Encode(resp); err != nil {
			return
		}
	}
}

func (s *Server) dispatch(req Request) Response {
	if req.Version != Version {
		return Response{OK: false, Code: apperr.ExitDaemon, Error: "unsupported protocol version"}
	}
	switch req.Method {
	case "GetStatus":
		st, err := s.handler.GetStatus()
		if err != nil {
			return errResp(err)
		}
		b, _ := json.Marshal(st)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		return Response{OK: true, Result: m}
	case "AcquireLease":
		var p AcquireParams
		if err := mapDecode(req.Params, &p); err != nil {
			return Response{OK: false, Code: apperr.ExitInvalidArgs, Error: err.Error()}
		}
		l, err := s.handler.AcquireLease(p)
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
		if err := s.handler.StartTunnel(); err != nil {
			return errResp(err)
		}
		return Response{OK: true}
	case "StopTunnel":
		if err := s.handler.StopTunnel(); err != nil {
			return errResp(err)
		}
		return Response{OK: true}
	default:
		return Response{OK: false, Code: apperr.ExitDaemon, Error: "unknown method"}
	}
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
