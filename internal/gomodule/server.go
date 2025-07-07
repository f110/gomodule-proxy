package gomodule

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"

	"github.com/gorilla/mux"
	"go.f110.dev/xerrors"
)

type ProxyServer struct {
	s     *http.Server
	rr    *httputil.ReverseProxy
	r     *mux.Router
	proxy *ModuleProxy

	debug bool
}

func NewProxyServer(addr string, upstream *url.URL, proxy *ModuleProxy, debug bool) *ProxyServer {
	s := &ProxyServer{
		r:     mux.NewRouter(),
		rr:    httputil.NewSingleHostReverseProxy(upstream),
		proxy: proxy,
		debug: debug,
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
	s.r.Use(middlewareAccessLog)
	if debug {
		s.r.Use(middlewareDebugInfo)
	}

	return s
}

func (s *ProxyServer) Start() error {
	log.Printf("Start listening %s", s.s.Addr)
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
		log.Printf("Faild to get version list: %+v", err)
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
		log.Printf("Failed to get module info: %+v", err)
		http.Error(w, "", http.StatusBadRequest)
		return
	}
	if err := json.NewEncoder(w).Encode(info); err != nil {
		log.Printf("Failed to encode to json: %v", err)
		return
	}
}

func (s *ProxyServer) mod(w http.ResponseWriter, req *http.Request, module, version string) {
	mod, err := s.proxy.GetGoMod(req.Context(), module, version)
	if err != nil {
		log.Printf("Failed to get go.mod: %+v", err)
		http.Error(w, "", http.StatusInternalServerError)
		return
	}
	_, err = io.WriteString(w, mod)
	if err != nil {
		log.Printf("Failed to write a buffer to ResponseWriter: %v", err)
	}
}

func (s *ProxyServer) zip(w http.ResponseWriter, req *http.Request, module, version string) {
	err := s.proxy.GetZip(req.Context(), w, module, version)
	if err != nil {
		log.Printf("Failed to create zip: %+v", err)
		http.Error(w, "", http.StatusInternalServerError)
		return
	}
}

func (s *ProxyServer) latest(w http.ResponseWriter, req *http.Request, module, _ string) {
	info, err := s.proxy.GetLatestVersion(req.Context(), module)
	if err != nil {
		log.Printf("Failed to get latest module version: %+v", err)
		http.Error(w, "", http.StatusBadRequest)
		return
	}
	if err := json.NewEncoder(w).Encode(info); err != nil {
		log.Printf("Failed to encode to json: %v", err)
		return
	}
}

func middlewareAccessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		log.Printf(
			"host:%s protocol:%s method:%s path:%s remote_addr:%s ua:%s",
			req.Host,
			req.Proto,
			req.Method,
			req.URL.Path,
			req.RemoteAddr,
			req.Header.Get("User-Agent"),
		)

		next.ServeHTTP(w, req)
	})
}

func middlewareDebugInfo(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		vars := mux.Vars(req)
		log.Printf("vars:%v", vars)

		next.ServeHTTP(w, req)
	})
}
