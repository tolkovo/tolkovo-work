package crypto

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// LocalKeys is the self-host KeyProvider: KEKs from the environment. The
// current key wraps new data keys; retired keys only unwrap, until a
// rotation job has rewrapped everything sealed with them.
type LocalKeys struct {
	current uint32
	keys    map[uint32][]byte
}

var _ KeyProvider = (*LocalKeys)(nil)

// ParseLocalKeys builds the keyring from TOLKOVO_MASTER_KEY (base64 of 32
// bytes, version currentVersion) and TOLKOVO_MASTER_KEYS_RETIRED: a comma
// separated list of "version:base64". Errors never contain key material.
func ParseLocalKeys(current string, currentVersion uint32, retired string) (*LocalKeys, error) {
	if currentVersion == 0 {
		return nil, errors.New("crypto: the master key version must be at least 1")
	}
	key, err := decodeKey(current)
	if err != nil {
		return nil, fmt.Errorf("crypto: master key: %w", err)
	}
	lk := &LocalKeys{current: currentVersion, keys: map[uint32][]byte{currentVersion: key}}
	for i, item := range strings.Split(retired, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		v, k, ok := strings.Cut(item, ":")
		if !ok {
			return nil, fmt.Errorf("crypto: retired key #%d must be version:base64", i+1)
		}
		n, err := strconv.ParseUint(v, 10, 32)
		if err != nil || n == 0 {
			return nil, fmt.Errorf("crypto: retired key #%d has an invalid version", i+1)
		}
		version := uint32(n)
		if _, dup := lk.keys[version]; dup {
			return nil, fmt.Errorf("crypto: key version %d is given twice", version)
		}
		key, err := decodeKey(k)
		if err != nil {
			return nil, fmt.Errorf("crypto: retired key version %d: %w", version, err)
		}
		lk.keys[version] = key
	}
	return lk, nil
}

func decodeKey(s string) ([]byte, error) {
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil {
		return nil, errors.New("not valid base64 (generate one with `tolkovo keygen`)")
	}
	if len(key) != KeySize {
		return nil, fmt.Errorf("must decode to %d bytes, got %d", KeySize, len(key))
	}
	return key, nil
}

// CurrentVersion implements KeyProvider.
func (k *LocalKeys) CurrentVersion() uint32 { return k.current }

// Wrap implements KeyProvider.
func (k *LocalKeys) Wrap(_ context.Context, dek, aad []byte) ([]byte, uint32, error) {
	wrapped, err := gcmSeal(k.keys[k.current], dek, aad)
	if err != nil {
		return nil, 0, err
	}
	return wrapped, k.current, nil
}

// Unwrap implements KeyProvider.
func (k *LocalKeys) Unwrap(_ context.Context, wrapped []byte, version uint32, aad []byte) ([]byte, error) {
	key, ok := k.keys[version]
	if !ok {
		return nil, fmt.Errorf("crypto: unknown key version %d", version)
	}
	dek, err := gcmOpen(key, wrapped, aad)
	if err != nil {
		return nil, ErrDecrypt
	}
	if len(dek) != KeySize {
		return nil, ErrDecrypt
	}
	return dek, nil
}
