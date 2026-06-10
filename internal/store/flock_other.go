//go:build !unix

package store

// Sans flock, le contrôle d'horodatage reste : la fenêtre de course se réduit
// à quelques microsecondes — accepté hors unix.
func lockSidecar(string) (func(), error) { return func() {}, nil }
