package config

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
)

// DevMasterKey is the example key of deploy/compose/.env.example. It is
// public, so production refuses it.
const DevMasterKey = "dG9sa292by1kZXYtb25seS1tYXN0ZXIta2V5LTAwMDE="

// masterKeySize matches crypto.KeySize; config does not import crypto to
// stay a leaf package.
const masterKeySize = 32

// MasterKey configures encryption of secrets at rest (docs/02 §13.3):
// TOLKOVO_MASTER_KEY and friends. Production refuses to start without it,
// since channels store secrets (T-01); in development an empty key only
// disables connecting channels.
type MasterKey struct {
	// Key is base64 of 32 random bytes (`tolkovo keygen`).
	Key Secret `env:"KEY"`
	// Version names Key; raise it on rotation.
	Version uint32 `env:"KEY_VERSION" envDefault:"1"`
	// Retired keys still open old values until they are rewrapped:
	// comma-separated "version:base64".
	Retired Secret `env:"KEYS_RETIRED"`
}

func (c Config) validateMasterKey() []error {
	m, p := c.MasterKey, Prefix+"MASTER_"
	var errs []error
	if m.Key == "" {
		if c.Env == EnvProduction {
			errs = append(errs, fmt.Errorf("%sKEY is required in production: it encrypts bot tokens and Telegram sessions (generate with `tolkovo keygen`)", p))
		}
		if m.Retired != "" {
			errs = append(errs, fmt.Errorf("%sKEYS_RETIRED is set but %sKEY is empty", p, p))
		}
		return errs
	}
	if m.Version == 0 {
		errs = append(errs, fmt.Errorf("%sKEY_VERSION must be at least 1", p))
	}
	if !validMasterKey(m.Key.Reveal()) {
		errs = append(errs, fmt.Errorf("%sKEY must be base64 of %d bytes (generate with `tolkovo keygen`)", p, masterKeySize))
	}
	if c.Env == EnvProduction && strings.TrimSpace(m.Key.Reveal()) == DevMasterKey {
		errs = append(errs, fmt.Errorf("%sKEY is the public example key: generate your own with `tolkovo keygen`", p))
	}
	for i, item := range strings.Split(m.Retired.Reveal(), ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		v, k, ok := strings.Cut(item, ":")
		n, err := strconv.ParseUint(v, 10, 32)
		if !ok || err != nil || n == 0 || !validMasterKey(k) {
			errs = append(errs, fmt.Errorf("%sKEYS_RETIRED item #%d must be version:base64 of %d bytes", p, i+1, masterKeySize))
			continue
		}
		if uint32(n) == m.Version {
			errs = append(errs, fmt.Errorf("%sKEYS_RETIRED item #%d reuses the current version %d", p, i+1, n))
		}
	}
	return errs
}

func validMasterKey(s string) bool {
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	return err == nil && len(b) == masterKeySize
}
