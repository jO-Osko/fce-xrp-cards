package extension

import (
	"errors"
	"fmt"
	"os"
	"strconv"
)

// Parsing, defaults and checks mirror tee-node v0.0.29 (internal/settings); re-check them on every tee-node bump.
const (
	extensionPortVar = "EXTENSION_PORT"
	signPortVar      = "SIGN_PORT"
	logLevelVar      = "LOG_LEVEL"

	defaultExtensionPort = 8889
	defaultSignPort      = 8888
	// tee-node's config port is fixed; neither port may take it
	configPort = 5500

	// go-flare-common's default; tee-node's is FATAL
	defaultLogLevel = "DEBUG"
)

// Env holds the node's variables the extension reads.
type Env struct {
	ExtensionPort int
	SignPort      int
	LogLevel      string // for go-flare-common's logger.Set
}

// ReadEnv reads Env from the process environment.
// Unset or empty variables take tee-node's defaults; DEBUG for LOG_LEVEL.
// A port set to a value tee-node rejects is an error.
func ReadEnv() (Env, error) {
	return parseEnv(os.Getenv)
}

func parseEnv(getenv func(string) string) (Env, error) {
	ext, extErr := portFromEnv(getenv, extensionPortVar, defaultExtensionPort)
	sign, signErr := portFromEnv(getenv, signPortVar, defaultSignPort)
	if err := errors.Join(extErr, signErr); err != nil {
		return Env{}, err
	}
	if ext == sign {
		return Env{}, fmt.Errorf("%s and %s are both %d", extensionPortVar, signPortVar, ext)
	}

	level := defaultLogLevel
	if v := getenv(logLevelVar); v != "" {
		level = v
	}

	return Env{ExtensionPort: ext, SignPort: sign, LogLevel: level}, nil
}

// portFromEnv takes no trimming and no port 0: tee-node's Atoi and range check.
func portFromEnv(getenv func(string) string, name string, fallback int) (int, error) {
	raw := getenv(name)
	if raw == "" {
		return fallback, nil
	}

	p, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s=%q is not a number", name, raw)
	}
	if p <= 0 || p > 65535 {
		return 0, fmt.Errorf("%s=%d is not a valid port", name, p)
	}
	if p == configPort {
		return 0, fmt.Errorf("%s=%d collides with tee-node's config port", name, p)
	}
	return p, nil
}
