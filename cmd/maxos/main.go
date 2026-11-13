package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/maxjkfc/maxos/internal/access"
	"github.com/maxjkfc/maxos/internal/config"
	"github.com/maxjkfc/maxos/internal/core"
	"github.com/maxjkfc/maxos/internal/db"
	"github.com/maxjkfc/maxos/internal/mcpserver"
)

const usage = "usage: maxos <migrate|serve|mcp-stdio>"

func main() {
	// stdout is the MCP channel in mcp-stdio mode; log to stderr only.
	log.SetOutput(os.Stderr)

	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var err error
	switch os.Args[1] {
	case "migrate":
		err = runMigrate(ctx)
	case "serve":
		err = runServe(ctx)
	case "mcp-stdio":
		err = runStdio(ctx)
	default:
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	if err != nil {
		log.Printf("maxos %s: %v", os.Args[1], err)
		os.Exit(1)
	}
}

func runMigrate(ctx context.Context) error {
	cfg, err := config.Load(false)
	if err != nil {
		return err
	}
	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := db.Migrate(ctx, pool); err != nil {
		return err
	}
	log.Print("migrations applied")
	return nil
}

func runServe(ctx context.Context) error {
	cfg, err := config.Load(true)
	if err != nil {
		return err
	}
	verifier, err := access.NewVerifier(ctx, cfg.AccessTeamDomain, cfg.AccessAUD)
	if err != nil {
		return err
	}
	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	inbox := core.NewInbox(pool)
	getServer := func(r *http.Request) *mcp.Server {
		// The middleware guarantees an identity; a missing one is a wiring bug,
		// so refuse to serve rather than fall back to an anonymous actor.
		id, ok := access.IdentityFrom(r.Context())
		if !ok {
			return nil
		}
		return mcpserver.New(inbox, "mcp-http", id.Actor())
	}
	mcpHandler := mcp.NewStreamableHTTPHandler(getServer, &mcp.StreamableHTTPOptions{Stateless: true})

	mux := http.NewServeMux()
	mux.Handle("/mcp", verifier.Middleware(mcpHandler))

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	log.Printf("listening on %s", cfg.HTTPAddr)

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}

	log.Print("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return err
	}
	if err := <-errc; !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func runStdio(ctx context.Context) error {
	cfg, err := config.Load(false)
	if err != nil {
		return err
	}
	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	return mcpserver.New(core.NewInbox(pool), "mcp-stdio", "local").Run(ctx, &mcp.StdioTransport{})
}
