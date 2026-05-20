package keystore

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
)

// Env loading conventions:
//
//	ATARA_PAY_MASTER_KEY_V1=<base64 of 32 random bytes>
//	ATARA_PAY_MASTER_KEY_V2=<base64 of 32 random bytes>
//	ATARA_PAY_KEYSTORE_CURRENT_VERSION=2   (optional; defaults to the
//	                                        highest version found)
//
// Generate a key locally with:
//
//	openssl rand -base64 32

const (
	envKeyPrefix    = "ATARA_PAY_MASTER_KEY_V"
	envCurrentName  = "ATARA_PAY_KEYSTORE_CURRENT_VERSION"
)

// FromEnv reads master keys from the environment and returns a configured
// AESKeystore. Returns an error if no master keys are present.
func FromEnv() (*AESKeystore, error) {
	keys := map[int16][]byte{}
	for _, kv := range os.Environ() {
		name, val, ok := strings.Cut(kv, "=")
		if !ok || !strings.HasPrefix(name, envKeyPrefix) {
			continue
		}
		versionStr := strings.TrimPrefix(name, envKeyPrefix)
		v64, err := strconv.ParseInt(versionStr, 10, 16)
		if err != nil {
			return nil, fmt.Errorf("keystore: malformed env var %q: %w", name, err)
		}
		decoded, err := base64.StdEncoding.DecodeString(val)
		if err != nil {
			return nil, fmt.Errorf("keystore: %s: base64 decode: %w", name, err)
		}
		keys[int16(v64)] = decoded
	}
	if len(keys) == 0 {
		return nil, errors.New("keystore: no ATARA_PAY_MASTER_KEY_V* env vars set")
	}

	current, err := pickCurrent(keys)
	if err != nil {
		return nil, err
	}
	return NewAESKeystore(keys, current)
}

// pickCurrent honors ATARA_PAY_KEYSTORE_CURRENT_VERSION when set, otherwise
// falls back to the highest version present. The fallback is what most
// deployments want: rotation just bumps the next version, sets that as
// CURRENT, and re-encrypts.
func pickCurrent(keys map[int16][]byte) (int16, error) {
	if v := os.Getenv(envCurrentName); v != "" {
		n, err := strconv.ParseInt(v, 10, 16)
		if err != nil {
			return 0, fmt.Errorf("keystore: %s: %w", envCurrentName, err)
		}
		if _, ok := keys[int16(n)]; !ok {
			return 0, fmt.Errorf("keystore: %s=%d but no ATARA_PAY_MASTER_KEY_V%d set",
				envCurrentName, n, n)
		}
		return int16(n), nil
	}

	versions := make([]int16, 0, len(keys))
	for v := range keys {
		versions = append(versions, v)
	}
	sort.Slice(versions, func(i, j int) bool { return versions[i] > versions[j] })
	return versions[0], nil
}
