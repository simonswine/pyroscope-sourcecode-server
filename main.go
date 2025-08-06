package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"connectrpc.com/connect"
	"github.com/grafana/pyroscope-sourcecode-server/sourceserver"
	"github.com/grafana/pyroscope/api/gen/proto/go/vcs/v1/vcsv1connect"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

func run() error {
	addr := flag.String("addr", ":8080", "address to listen on")
	repoBase := flag.String("repo-base", "/tmp/repos", "base directory for git repositories")
	verbose := flag.Bool("verbose", true, "enable verbose logging")
	help := flag.Bool("help", false, "show help")
	flag.Parse()

	lvl := slog.LevelInfo
	if *verbose {
		lvl = slog.LevelDebug
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: lvl}))
	logger.Info("starting server", "addr", *addr, "repo-base", *repoBase, "verbose", *verbose)

	if help != nil && *help {
		flag.PrintDefaults()
		return nil
	}

	if err := os.MkdirAll(*repoBase, 0o755); err != nil {
		return fmt.Errorf("mkdir all: %w", err)
	}

	mux := http.NewServeMux()
	compress1KB := connect.WithCompressMinBytes(1024)
	mux.Handle(vcsv1connect.NewVCSServiceHandler(
		sourceserver.New(*repoBase, logger),
		compress1KB,
	))

	srv := &http.Server{
		Addr: *addr,
		Handler: h2c.NewHandler(
			mux,
			&http2.Server{},
		),
		ReadHeaderTimeout: time.Second,
		ReadTimeout:       5 * time.Minute,
		WriteTimeout:      5 * time.Minute,
		MaxHeaderBytes:    8 * 1024, // 8KiB
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("HTTP listen and serve: %v", err)
		}
	}()

	<-signals
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Fatalf("HTTP shutdown: %v", err) //nolint:gocritic
	}
	return nil
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}
