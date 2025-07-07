package gomodule

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"

	"github.com/go-logr/logr"
	"github.com/gorilla/mux"
	"go.f110.dev/xerrors"
)

type ProxyServer struct {
	s     *http.Server
	rr    *httputil.ReverseProxy
	r     *mux.Router
	proxy *ModuleProxy

	logger logr.Logger
	debug  bool
}

func NewProxyServer(addr string, upstream *url.URL, proxy *ModuleProxy, logger logr.Logger, debug bool) *ProxyServer {
	s := &ProxyServer{
		r:      mux.NewRouter(),
		rr:     httputil.NewSingleHostReverseProxy(upstream),
		proxy:  proxy,
		logger: logger,
		debug:  debug,
	}
	s.s = &http.Server{
		Addr:    addr,
		Handler: s.r,
	}

	s.r.Methods(http.MethodGet).Path("/{module:.+}/@v/list").HandlerFunc(s.handle(s.list))
	s.r.Methods(http.MethodGet).Path("/{module:.+}/@v/{version}.info").HandlerFunc(s.handle(s.info))
	s.r.Methods(http.MethodGet).Path("/{module:.+}/@v/{version}.mod").HandlerFunc(s.handle(s.mod))
	s.r.Methods(http.MethodGet).Path("/{module:.+}/@v/{version}.zip").HandlerFunc(s.handle(s.zip))
	s.r.Methods(http.MethodGet).Path("/{module:.+}/@latest").HandlerFunc(s.handle(s.latest))
	s.r.Use(middlewareAccessLog(logger.WithName("access_log")))
	if debug {
		s.r.Use(middlewareDebugInfo)
	}

	return s
}

func (s *ProxyServer) Start() error {
	s.logger.Info("Starting listening", "addr", s.s.Addr)
	if err := s.s.ListenAndServe(); err != nil {
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}

		return xerrors.WithStack(err)
	}

	return nil
}

func (s *ProxyServer) Stop(ctx context.Context) error {
	if err := s.s.Shutdown(ctx); err != nil {
		return xerrors.WithStack(err)
	}
	return nil
}

func (s *ProxyServer) handle(h func(w http.ResponseWriter, req *http.Request, module, version string)) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		vars := mux.Vars(req)
		if v, ok := vars["module"]; !ok || v == "" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if s.proxy.IsProxy(vars["module"]) {
			h(w, req, vars["module"], vars["version"])
			return
		}

		s.rr.ServeHTTP(w, req)
	}
}

func (s *ProxyServer) list(w http.ResponseWriter, req *http.Request, module, _ string) {
	vers, err := s.proxy.Versions(req.Context(), module)
	if err != nil {
		s.logger.Info("Failed to get versions", "err", err)
		http.Error(w, "", http.StatusNotFound)
		return
	}

	for _, v := range vers {
		fmt.Fprintln(w, v)
	}
}

func (s *ProxyServer) info(w http.ResponseWriter, req *http.Request, module, version string) {
	info, err := s.proxy.GetInfo(req.Context(), module, version)
	if err != nil {
		s.logger.Info("Failed to get module info", "err", err)
		http.Error(w, "", http.StatusBadRequest)
		return
	}
	if err := json.NewEncoder(w).Encode(info); err != nil {
		s.logger.Info("Failed to encode to json", "err", err)
		return
	}
}

func (s *ProxyServer) mod(w http.ResponseWriter, req *http.Request, module, version string) {
	mod, err := s.proxy.GetGoMod(req.Context(), module, version)
	if err != nil {
		s.logger.Info("Failed to get go.mod", "err", err)
		http.Error(w, "", http.StatusInternalServerError)
		return
	}
	_, err = io.WriteString(w, mod)
	if err != nil {
		s.logger.Info("Failed to write a buffer to ResponseWriter", "err", err)
	}
}

func (s *ProxyServer) zip(w http.ResponseWriter, req *http.Request, module, version string) {
	err := s.proxy.GetZip(req.Context(), w, module, version)
	if err != nil {
		s.logger.Info("Failed to create zip", "err", err)
		http.Error(w, "", http.StatusInternalServerError)
		return
	}
}

func (s *ProxyServer) latest(w http.ResponseWriter, req *http.Request, module, _ string) {
	info, err := s.proxy.GetLatestVersion(req.Context(), module)
	if err != nil {
		s.logger.Info("Failed to get latest module version", "err", err)
		http.Error(w, "", http.StatusBadRequest)
		return
	}
	if err := json.NewEncoder(w).Encode(info); err != nil {
		s.logger.Info("Failed to encode to json", xerrors.ZapField(err))
		return
	}
}

func middlewareAccessLog(logger logr.Logger) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			logger.Info(
				"",
				"host",
				req.Host,
				"protocol",
				req.Proto,
				"method",
				req.Method,
				"path",
				req.URL.Path,
				"remote_addr",
				req.RemoteAddr,
				"ua",
				req.Header.Get("User-Agent"),
			)

			next.ServeHTTP(w, req)
		})
	}
}

func middlewareDebugInfo(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		vars := mux.Vars(req)
		fmt.Printf("vars:%v\n", vars)

		next.ServeHTTP(w, req)
	})
}
