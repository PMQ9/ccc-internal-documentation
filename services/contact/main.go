// Command contact is the CCC Wiki contact-form service: it serves a branded
// form and, on submit, emails the destination mailbox and (optionally) files a
// GitHub issue. It owns no database — each submission is one email + one issue.
//
// Transport is config-driven (see Config): "agentmail" (recommended) sends via
// an agentmail.to inbox; "smtp" (the default) covers Brevo/Gmail/SES/Proton-
// Bridge; "graph" covers Microsoft 365 send-as. See docs/runbooks/contact-form.md.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	// `contact -healthcheck` probes the running server and exits 0/1. This is the
	// container HEALTHCHECK: the distroless image has no shell (so no curl), and
	// the binary is already present. With no args (the normal ENTRYPOINT) the flag
	// defaults false and we serve. (issue #43)
	healthcheck := flag.Bool("healthcheck", false, "probe the local server's /healthz and exit 0/1")
	flag.Parse()
	if *healthcheck {
		os.Exit(runHealthcheck())
	}

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

// runHealthcheck GETs the local server's /healthz and maps it to an exit code,
// giving the shell-less distroless container a HEALTHCHECK probe. It dials the
// configured listen port on the loopback. (issue #43)
func runHealthcheck() int {
	_, port, err := net.SplitHostPort(env("CONTACT_LISTEN", ":8080"))
	if err != nil || port == "" {
		port = "8080"
	}
	if err := probeHealth("http://127.0.0.1:"+port+"/healthz", 3*time.Second); err != nil {
		fmt.Fprintln(os.Stderr, "healthcheck:", err)
		return 1
	}
	return 0
}

func probeHealth(url string, timeout time.Duration) error {
	resp, err := (&http.Client{Timeout: timeout}).Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unhealthy: status %d", resp.StatusCode)
	}
	return nil
}
