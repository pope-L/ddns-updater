package health

import (
	"context"
	"net/http"
	"time"

	"github.com/qdm12/golibs/logging"
)

type Server struct {
	address string
	logger  logging.Logger
	handler http.Handler
}

func NewServer(address string, logger logging.Logger, healthcheck func() error) *Server {
	handler := newHandler(logger, healthcheck)
	return &Server{
		address: address,
		logger:  logger,
		handler: handler,
	}
}

func (s *Server) Run(ctx context.Context, done chan<- struct{}) {
	defer close(done)
	server := http.Server{
		Addr:              s.address,
		Handler:           s.handler,
		ReadHeaderTimeout: time.Second,
		ReadTimeout:       time.Second,
	}
	go func() {
		<-ctx.Done()
		s.logger.Warn("shutting down (context canceled)")
		defer s.logger.Warn("shut down")
		const shutdownGraceDuration = 2 * time.Second
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGraceDuration)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			s.logger.Error("failed shutting down: " + err.Error())
		}
	}()
	for ctx.Err() == nil {
		s.logger.Info("listening on " + s.address)
		err := server.ListenAndServe()
		if err != nil && ctx.Err() == nil { // server crashed
			s.logger.Error(err.Error())
			s.logger.Info("restarting")
		}
	}
}
