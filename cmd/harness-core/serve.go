package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"os"

	"github.com/jgulick48/claude-harness/internal/api"
	"github.com/jgulick48/claude-harness/internal/webserve"
)

// runServe is `harness-core serve` (docs/WEB.md). webserve points HOME and
// XDG_CONFIG_HOME at the data dir before NewAPI runs, so the stores below land
// there.
func runServe(ctx context.Context, args []string, stderr io.Writer) int {
	return webserve.Serve(ctx, args, stderr, os.LookupEnv, webserve.Deps{
		NewAPI: func(token string, logger *slog.Logger, withLocal bool) (http.Handler, io.Closer, error) {
			hm, err := newHostManagerOpts(logger, os.Getenv, withLocal)
			if err != nil {
				return nil, nil, err
			}
			h := api.NewServer(token, logger, hm, newRecentStore(logger, os.Getenv),
				api.WithUsageHistory(newUsageHistory(logger, os.Getenv)))
			return h, hm, nil
		},
	})
}
