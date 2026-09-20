package cri

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"k8s.io/klog/v2"
)

// Server serves the CRI subset as JSON over a unix socket.
type Server struct {
	Runtime RuntimeService
	Socket  string
	httpSrv *http.Server
}

func (s *Server) Start() error {
	if err := os.MkdirAll(filepath.Dir(s.Socket), 0755); err != nil {
		return err
	}
	_ = os.Remove(s.Socket)
	ln, err := net.Listen("unix", s.Socket)
	if err != nil {
		return err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/v1/RunPodSandbox", s.wrap(s.runPodSandbox))
	mux.HandleFunc("/v1/StopPodSandbox", s.wrap(s.stopPodSandbox))
	mux.HandleFunc("/v1/RemovePodSandbox", s.wrap(s.removePodSandbox))
	mux.HandleFunc("/v1/PodSandboxStatus", s.wrap(s.podSandboxStatus))
	mux.HandleFunc("/v1/CreateContainer", s.wrap(s.createContainer))
	mux.HandleFunc("/v1/StartContainer", s.wrap(s.startContainer))
	mux.HandleFunc("/v1/StopContainer", s.wrap(s.stopContainer))
	mux.HandleFunc("/v1/RemoveContainer", s.wrap(s.removeContainer))
	mux.HandleFunc("/v1/ContainerStatus", s.wrap(s.containerStatus))
	mux.HandleFunc("/v1/ExecSync", s.wrap(s.execSync))
	mux.HandleFunc("/v1/Logs", s.wrap(s.logs))
	mux.HandleFunc("/v1/ExecTTY", s.execTTY)
	mux.HandleFunc("/v1/ListPodSandbox", s.wrap(s.listPodSandbox))
	mux.HandleFunc("/v1/ListContainers", s.wrap(s.listContainers))
	s.httpSrv = &http.Server{Handler: mux}
	klog.Infof("marooned CRI shim listening on %s", s.Socket)
	return s.httpSrv.Serve(ln)
}

func (s *Server) wrap(fn func(http.ResponseWriter, *http.Request)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := recoverHandler(fn, w, r); err != nil {
			writeErr(w, err)
		}
	}
}

func recoverHandler(fn func(http.ResponseWriter, *http.Request), w http.ResponseWriter, r *http.Request) (err error) {
	defer func() {
		if rec := recover(); rec != nil {
			err = fmt.Errorf("panic: %v", rec)
		}
	}()
	fn(w, r)
	return nil
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, err error) {
	code := http.StatusInternalServerError
	if err == ErrUnimplemented {
		code = http.StatusNotImplemented
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

func decode(r *http.Request, v interface{}) error {
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(v)
}

func (s *Server) runPodSandbox(w http.ResponseWriter, r *http.Request) {
	var req RunPodSandboxRequest
	if err := decode(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Minute)
	defer cancel()
	sb, err := s.Runtime.RunPodSandbox(ctx, &req)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, sb)
}

type idReq struct {
	ID      string `json:"id"`
	Timeout int64  `json:"timeout"`
}

func (s *Server) stopPodSandbox(w http.ResponseWriter, r *http.Request) {
	var req idReq
	if err := decode(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	if err := s.Runtime.StopPodSandbox(r.Context(), req.ID); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, map[string]string{"status": "ok"})
}

func (s *Server) removePodSandbox(w http.ResponseWriter, r *http.Request) {
	var req idReq
	if err := decode(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	if err := s.Runtime.RemovePodSandbox(r.Context(), req.ID); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, map[string]string{"status": "ok"})
}

func (s *Server) podSandboxStatus(w http.ResponseWriter, r *http.Request) {
	var req idReq
	if err := decode(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	sb, err := s.Runtime.PodSandboxStatus(r.Context(), req.ID)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, sb)
}

type createReq struct {
	SandboxID string                 `json:"sandboxID"`
	Container CreateContainerRequest `json:"container"`
}

func (s *Server) createContainer(w http.ResponseWriter, r *http.Request) {
	var req createReq
	if err := decode(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	c, err := s.Runtime.CreateContainer(r.Context(), req.SandboxID, &req.Container)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, c)
}

func (s *Server) startContainer(w http.ResponseWriter, r *http.Request) {
	var req idReq
	if err := decode(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	if err := s.Runtime.StartContainer(r.Context(), req.ID); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, map[string]string{"status": "ok"})
}

func (s *Server) stopContainer(w http.ResponseWriter, r *http.Request) {
	var req idReq
	if err := decode(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	if err := s.Runtime.StopContainer(r.Context(), req.ID, time.Duration(req.Timeout)*time.Second); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, map[string]string{"status": "ok"})
}

func (s *Server) removeContainer(w http.ResponseWriter, r *http.Request) {
	var req idReq
	if err := decode(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	if err := s.Runtime.RemoveContainer(r.Context(), req.ID); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, map[string]string{"status": "ok"})
}

func (s *Server) containerStatus(w http.ResponseWriter, r *http.Request) {
	var req idReq
	if err := decode(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	c, err := s.Runtime.ContainerStatus(r.Context(), req.ID)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, c)
}

type execReq struct {
	ID      string   `json:"id"`
	Command []string `json:"command"`
	Timeout int64    `json:"timeout"`
}

func (s *Server) execSync(w http.ResponseWriter, r *http.Request) {
	var req execReq
	if err := decode(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	stdout, stderr, code, err := s.Runtime.ExecSync(r.Context(), req.ID, req.Command, time.Duration(req.Timeout)*time.Second)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, map[string]interface{}{"stdout": string(stdout), "stderr": string(stderr), "exitCode": code})
}

func (s *Server) logs(w http.ResponseWriter, r *http.Request) {
	var req idReq
	if err := decode(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	data, err := s.Runtime.Logs(r.Context(), req.ID)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, map[string]string{"data": data})
}

func (s *Server) execTTY(w http.ResponseWriter, r *http.Request) {
	var req execReq
	if err := decode(r, &req); err != nil {
		writeErr(w, err)
		return
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		writeErr(w, fmt.Errorf("no hijack"))
		return
	}
	conn, bufrw, err := hj.Hijack()
	if err != nil {
		return
	}
	defer conn.Close()
	_, _ = bufrw.WriteString("HTTP/1.1 200 OK\r\nContent-Type: application/octet-stream\r\nConnection: close\r\n\r\n")
	_ = bufrw.Flush()
	_ = s.Runtime.ExecTTY(r.Context(), req.ID, req.Command, conn)
}

func (s *Server) listPodSandbox(w http.ResponseWriter, r *http.Request) {
	list, err := s.Runtime.ListPodSandbox(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, list)
}

func (s *Server) listContainers(w http.ResponseWriter, r *http.Request) {
	list, err := s.Runtime.ListContainers(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, list)
}
