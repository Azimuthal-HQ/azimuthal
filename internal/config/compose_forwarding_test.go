package config_test

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The bundled Compose file declares no `env_file:`, so its `environment:` block
// is the ONLY channel from an operator's .env into the container. A setting the
// binary reads and the block does not name is unreachable — and unreachable
// SILENTLY, because from the application's side nothing was ever set.
//
// That is not a theoretical failure. Before this guard existed the file
// forwarded none of the ten AZIMUTHAL_* settings, two of which are security
// policy: an operator who put AZIMUTHAL_TICKET_REF_REQUIRED=true or
// AZIMUTHAL_BCRYPT_COST=14 in .env, restarted, and saw a clean startup would
// reasonably conclude the policy was in force. It was not.
//
// Nothing about that is caught by a build, a scan, or a boot: the server starts
// happily on its defaults. So the check is structural, and it reads BOTH real
// files rather than a list somebody has to remember to update.

const (
	configSource = "config.go"
	composeFile  = "../../build/docker-compose.yml"
)

// configKeyPattern matches the environment keys internal/config actually reads:
// viper lookups and the one raw os.Getenv (SMTP_HOST, read raw on purpose so it
// can tell an explicit relay from the localhost default).
var configKeyPattern = regexp.MustCompile(`(?:v\.Get(?:String|Bool|Int|Duration)|os\.Getenv)\("([A-Z][A-Z0-9_]*)"\)`)

// keysReadByConfig extracts every env key from the config source itself, so the
// guard cannot drift from the code the way a hand-maintained list would.
func keysReadByConfig(t *testing.T) map[string]bool {
	t.Helper()
	src, err := os.ReadFile(configSource)
	if err != nil {
		t.Fatalf("reading %s: %v", configSource, err)
	}
	keys := map[string]bool{}
	for _, m := range configKeyPattern.FindAllStringSubmatch(string(src), -1) {
		keys[m[1]] = true
	}
	if len(keys) < 20 {
		t.Fatalf("only %d keys extracted from %s — the pattern has probably stopped "+
			"matching, and a guard that matches nothing passes everything", len(keys), configSource)
	}
	return keys
}

// appEnvironment returns the app service's environment block as key -> raw
// value. Hand-scanned rather than YAML-parsed to keep internal/config free of
// dependencies; the block is fixed-indentation and the parse fails loudly.
func appEnvironment(t *testing.T) map[string]string {
	t.Helper()
	raw, err := os.ReadFile(composeFile)
	if err != nil {
		t.Fatalf("reading %s: %v", composeFile, err)
	}
	lines := strings.Split(string(raw), "\n")

	start := -1
	for i, l := range lines {
		if strings.TrimSpace(l) == "environment:" {
			start = i + 1
			break
		}
	}
	if start == -1 {
		t.Fatalf("no environment: block found in %s", composeFile)
	}

	entry := regexp.MustCompile(`^\s{6}([A-Z][A-Z0-9_]*):\s*(.*)$`)
	env := map[string]string{}
	for _, l := range lines[start:] {
		trimmed := strings.TrimSpace(l)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		m := entry.FindStringSubmatch(l)
		if m == nil {
			break // dedent — end of the app service's environment block
		}
		env[m[1]] = strings.TrimSpace(m[2])
	}
	if len(env) == 0 {
		t.Fatalf("parsed an empty environment block from %s", composeFile)
	}
	return env
}

// composeManaged are the settings the Compose file supplies itself, because
// they address the sibling services or fix the in-container shape. They are
// exempt from the bare-${KEY} rule below, and only from that rule.
var composeManaged = map[string]string{
	"DATABASE_URL":       "addresses the db service",
	"STORAGE_ENDPOINT":   "addresses the storage service",
	"STORAGE_ACCESS_KEY": "composed from MINIO_ROOT_USER",
	"STORAGE_SECRET_KEY": "composed from MINIO_ROOT_PASSWORD",
	"APP_ENV":            "the bundled stack is a production deployment",
	"APP_PORT":           "the in-container port is fixed; the host port is the ports: mapping",
}

func TestComposeForwardsEverySetting(t *testing.T) {
	want := keysReadByConfig(t)
	got := appEnvironment(t)

	var missing []string
	for key := range want {
		if _, ok := got[key]; !ok {
			missing = append(missing, key)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("build/docker-compose.yml does not forward %d setting(s) that internal/config "+
			"reads: %v\n\nA setting missing here cannot be set by an operator at all — the file "+
			"declares no env_file:, so this block is the only way in, and the failure is silent.",
			len(missing), missing)
	}
}

// The other direction. A key forwarded here that nothing reads is a promise the
// application does not keep, which is the same defect pointing the other way.
func TestComposeForwardsNothingUnread(t *testing.T) {
	want := keysReadByConfig(t)
	got := appEnvironment(t)

	var unread []string
	for key := range got {
		if !want[key] {
			unread = append(unread, key)
		}
	}
	sort.Strings(unread)
	if len(unread) > 0 {
		t.Errorf("build/docker-compose.yml forwards %d setting(s) internal/config never reads: %v\n\n"+
			"Either wire it up or drop it — forwarding a setting nothing reads tells an operator "+
			"it works.", len(unread), unread)
	}
}

// Operator-facing settings are forwarded BARE — `${KEY}`, never
// `${KEY:-default}`.
//
// viper runs without AllowEmptyEnv, so a present-but-empty variable (what bare
// forwarding produces when .env omits the key) is treated as unset and falls
// through to the default in this package. Repeating a default in the Compose
// file therefore buys nothing and creates a second place for it to be wrong.
//
// It HAS been wrong: the file sent `SMTP_PORT: ${SMTP_PORT:-25}` while this
// package defaulted to 1025, so the bundled stack silently ran on a different
// port than the documentation promised. And `SMTP_HOST: ${SMTP_HOST:-localhost}`
// was worse than a divergence — validate() reads SMTP_HOST with a raw
// os.Getenv precisely to tell "an operator configured a relay" from "nobody
// set anything", and an unconditional localhost defeated the check that refuses
// email delivery without a relay.
func TestComposeForwardsOperatorSettingsWithoutADefault(t *testing.T) {
	for key, value := range appEnvironment(t) {
		if reason, managed := composeManaged[key]; managed {
			t.Logf("%s is Compose-managed (%s) — exempt", key, reason)
			continue
		}
		if want := "${" + key + "}"; value != want {
			t.Errorf("%s is forwarded as %q; expected the bare %q.\n"+
				"A ${KEY:-default} here duplicates the default in this package and can drift "+
				"from it. An empty value already falls through to that default.", key, value, want)
		}
	}
}
