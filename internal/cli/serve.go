package cli

import (
	"fmt"
	"net"
	"net/http"

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
			srv := web.NewServer(f, a.marketSource(), a.offline)
			fmt.Fprintf(cmd.OutOrStdout(), "finador sur http://%s\n", addr)
			return http.ListenAndServe(addr, srv.Handler())
		},
	}
	cmd.Flags().StringVar(&addr, "addr", "127.0.0.1:8451", "adresse d'écoute")
	return cmd
}
