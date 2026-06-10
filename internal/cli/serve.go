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
		Use:   "serve",
		Short: "Sert l'application web (mot de passe demandé au lancement)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			host, _, err := net.SplitHostPort(addr)
			if err != nil {
				return fmt.Errorf("adresse %q invalide: %w", addr, err)
			}
			f, err := a.open()
			if err != nil {
				return err
			}
			a.ensureFresh(cmd, f)
			if host != "127.0.0.1" && host != "localhost" && host != "::1" {
				fmt.Fprintf(cmd.ErrOrStderr(),
					"ATTENTION : %s expose votre patrimoine au-delà de cette machine (aucune authentification web)\n", addr)
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			httpSrv := &http.Server{Addr: addr, Handler: web.NewServer(f, a.marketSource(), a.offline).Handler()}
			errc := make(chan error, 1)
			go func() { errc <- httpSrv.ListenAndServe() }()
			fmt.Fprintf(cmd.OutOrStdout(), "finador sur http://%s — Ctrl-C pour arrêter\n", addr)
			select {
			case err := <-errc:
				return err
			case <-ctx.Done():
				fmt.Fprintln(cmd.OutOrStdout(), "\narrêt…")
				shCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				return httpSrv.Shutdown(shCtx)
			}
		},
	}
	cmd.Flags().StringVar(&addr, "addr", "127.0.0.1:8451", "adresse d'écoute")
	return cmd
}
