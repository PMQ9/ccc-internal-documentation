// Command contact is the CCC Wiki contact-form service: it serves a branded
// form and, on submit, emails the destination mailbox and (optionally) files a
// GitHub issue. It owns no database — each submission is one email + one issue.
//
// Transport is config-driven (see Config): "smtp" covers Brevo/Gmail/SES/
// Proton-Bridge; "graph" covers Microsoft 365 send-as. See
// docs/runbooks/contact-form.md.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg, err := Load()
	if err != nil {
		log.Error("invalid configuration", "err", err)
		os.Exit(1)
	}

	srv, err := newServer(cfg, log)
	if err != nil {
		log.Error("startup failed", "err", err)
		os.Exit(1)
	}

	httpSrv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           srv.routes(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	// Graceful shutdown: stop accepting, finish in-flight, then exit.
	idle := make(chan struct{})
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		<-sig
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := httpSrv.Shutdown(ctx); err != nil {
			log.Error("graceful shutdown failed", "err", err)
		}
		close(idle)
	}()

	log.Info("contact service listening",
		"addr", cfg.Listen,
		"transport", cfg.Transport,
		"mail_ready", cfg.mailConfigured(),
		"github_enabled", cfg.githubConfigured())

	if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("server error", "err", err)
		os.Exit(1)
	}
	<-idle
	log.Info("contact service stopped")
}
