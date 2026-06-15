package cli

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"finador/internal/web"
)

func serveCmd(a *app) *cobra.Command {
	var addr string
	cmd := &cobra.Command{
		Use:     "serve",
		Short:   "Serve the web application (password prompted at startup)",
		Example: "  finador serve",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			host, _, err := net.SplitHostPort(addr)
			if err != nil {
				return fmt.Errorf("invalid address %q: %w", addr, err)
			}
			f, err := a.open()
			if err != nil {
				return err
			}
			a.ensureFresh(cmd, f)
			if host != "127.0.0.1" && host != "localhost" && host != "::1" {
				fmt.Fprintf(cmd.ErrOrStderr(),
					"WARNING: %s exposes your portfolio beyond this machine (no web authentication)\n", addr)
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			httpSrv := &http.Server{Addr: addr, Handler: web.NewServer(f, a.marketSource(), a.offline).Handler()}
			errc := make(chan error, 1)
			go func() { errc <- httpSrv.ListenAndServe() }()
			fmt.Fprintf(cmd.OutOrStdout(), "finador on http://%s - Ctrl-C to stop\n", addr)
			select {
			case err := <-errc:
				return err
			case <-ctx.Done():
				fmt.Fprintln(cmd.OutOrStdout(), "\nshutting down…")
				shCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				return httpSrv.Shutdown(shCtx)
			}
		},
	}
	cmd.Flags().StringVar(&addr, "addr", "127.0.0.1:8451", "listen address")
	return cmd
}
