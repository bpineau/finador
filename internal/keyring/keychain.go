package keyring

import (
	"encoding/base64"
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
// via /usr/bin/security - no CGo. The expiry is fixed at Put time.
type keychain struct {
	now func() time.Time
	run func(args ...string) (string, error)
}

func runSecurity(args ...string) (string, error) {
	out, err := exec.Command("/usr/bin/security", args...).Output()
	return strings.TrimSuffix(string(out), "\n"), err
}

func (k *keychain) Get(key string) (string, bool) {
	enc, err := k.run("find-generic-password", "-s", service, "-a", key, "-w")
	if err != nil {
		return "", false
	}
	raw, derr := base64.StdEncoding.DecodeString(strings.TrimSpace(enc))
	if derr != nil {
		return "", false // unreadable entry (old format): retype, then re-cache
	}
	stamp, password, ok := strings.Cut(string(raw), "\n")
	expiry, perr := strconv.ParseInt(stamp, 10, 64)
	if !ok || perr != nil || k.now().After(time.Unix(expiry, 0)) {
		return "", false
	}
	return password, true
}

func (k *keychain) Put(key, password string, ttl time.Duration) {
	// Encode the "expiry\npassword" payload in base64 so the stored value is
	// always printable (no \n). Without this, `security find-generic-password -w`
	// returns a HEX DUMP as soon as the value contains a non-printable byte (the \n),
	// which broke reading it back - and thus the password cache.
	payload := fmt.Sprintf("%d\n%s", k.now().Add(ttl).Unix(), password)
	enc := base64.StdEncoding.EncodeToString([]byte(payload))
	// -U updates the entry if it exists; failure is benign (we'll retype).
	// The payload (base64) goes through argv (a brief window in ps): an accepted
	// trade-off of a CGo-free design - security(1) has no clean stdin read.
	_, _ = k.run("add-generic-password", "-U", "-s", service, "-a", key, "-w", enc)
}

// Purge deletes every finador entry; security removes one match per call.
func (k *keychain) Purge() {
	for {
		if _, err := k.run("delete-generic-password", "-s", service); err != nil {
			return
		}
	}
}
