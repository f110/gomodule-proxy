package main

import (
	"context"
	"log"
	"net/http"

	"github.com/julienschmidt/httprouter"
	"golang.org/x/xerrors"
)

type proxyServer struct {
	s *http.Server
	r *httprouter.Router
}

func newProxyServer(addr string) *proxyServer {
	s := &proxyServer{
		r: httprouter.New(),
	}
	s.s = &http.Server{
		Addr:    addr,
		Handler: s.r,
	}

	return s
}

func (s *proxyServer) Start() error {
	log.Printf("Start listening %s", s.s.Addr)
	if err := s.s.ListenAndServe(); err != nil {
		if err == http.ErrServerClosed {
			return nil
		}

		return xerrors.Errorf(": %w", err)
	}

	return nil
}

func (s *proxyServer) Stop(ctx context.Context) error {
	return s.s.Shutdown(ctx)
}
