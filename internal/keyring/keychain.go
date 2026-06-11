package keyring

import (
	"fmt"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// System returns the platform cache: macOS Keychain entries, a no-op elsewhere.
func System() Cache {
	if runtime.GOOS != "darwin" {
		return Disabled()
	}
	return &keychain{now: time.Now, run: runSecurity}
}

const service = "finador"

// keychain stores "expiry-unix\npassword" as a Keychain generic password,
// via /usr/bin/security — no CGo. The expiry is fixed at Put time.
type keychain struct {
	now func() time.Time
	run func(args ...string) (string, error)
}

func runSecurity(args ...string) (string, error) {
	out, err := exec.Command("/usr/bin/security", args...).Output()
	return strings.TrimSuffix(string(out), "\n"), err
}

func (k *keychain) Get(key string) (string, bool) {
	payload, err := k.run("find-generic-password", "-s", service, "-a", key, "-w")
	if err != nil {
		return "", false
	}
	stamp, password, ok := strings.Cut(payload, "\n")
	expiry, perr := strconv.ParseInt(stamp, 10, 64)
	if !ok || perr != nil || k.now().After(time.Unix(expiry, 0)) {
		return "", false
	}
	return password, true
}

func (k *keychain) Put(key, password string, ttl time.Duration) {
	payload := fmt.Sprintf("%d\n%s", k.now().Add(ttl).Unix(), password)
	// -U met à jour l'entrée si elle existe ; l'échec est bénin (on retapera).
	// Le payload passe en argv (brève fenêtre de visibilité dans ps) : compromis
	// assumé d'un design sans CGo — security(1) n'a pas de lecture stdin propre.
	_, _ = k.run("add-generic-password", "-U", "-s", service, "-a", key, "-w", payload)
}

// Purge deletes every finador entry; security removes one match per call.
func (k *keychain) Purge() {
	for {
		if _, err := k.run("delete-generic-password", "-s", service); err != nil {
			return
		}
	}
}
