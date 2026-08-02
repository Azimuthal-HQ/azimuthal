package config_test

import (
	"testing"

	"github.com/Azimuthal-HQ/azimuthal/internal/config"
)

// Disclosure of the customer-portal sign-in URL, as a truth table.
//
// POST /portal/{portalKey}/auth/request-link is unauthenticated by necessity —
// an external requester has no credential yet — so a response body carrying the
// sign-in URL lets anybody sign in as any address they can name. It is not a
// misconfiguration to warn about; it is a total authentication bypass.
//
// The rule that used to decide it lived in cmd/server/main.go as
// `cfg.PortalLinkDelivery == config.PortalLinkDeliveryLink && !cfg.IsProduction()`.
// BOTH operands were defaults — "link" and "development" — so the unsafe state
// was the one an operator reached by doing nothing, and cmd/server had no test,
// so nothing in the tree asserted the production off-branch at all. That is the
// gap these tests close.
//
// EMPTY MEANS UNSET. viper runs with AllowEmptyEnv off, so an environment
// variable set to "" is treated as absent and the default applies. Every row
// below with an empty string is therefore exercising the DEFAULT, which is the
// only reason the stock-install row means anything.

// discloseCase is one (APP_ENV, AZIMUTHAL_PORTAL_DISCLOSE_LINK) pair.
type discloseCase struct {
	name   string
	appEnv string // "" — unset, so the APP_ENV default applies
	flag   string // "" — unset, so the flag default applies
	want   bool
	why    string
}

// load builds a Config from the case's environment. DATABASE_URL is the one
// genuinely required setting; everything else is left to its default so that a
// row saying "unset" really is unset.
func (c discloseCase) load(t *testing.T) *config.Config {
	t.Helper()
	t.Setenv("DATABASE_URL", "postgres://user:pass@localhost:5432/db")
	t.Setenv("APP_ENV", c.appEnv)
	t.Setenv("AZIMUTHAL_PORTAL_DISCLOSE_LINK", c.flag)

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("APP_ENV=%q AZIMUTHAL_PORTAL_DISCLOSE_LINK=%q must load: %v", c.appEnv, c.flag, err)
	}
	return cfg
}

// TestPortalLinkDisclosureAllowed_TruthTable is the whole security claim: no
// combination of settings discloses in production, and no combination discloses
// outside production without the flag.
//
// It fails in both directions by construction. Delete `!c.IsProduction()` from
// PortalLinkDisclosureAllowed and the three production rows with the flag on
// fail; delete `c.PortalDiscloseLink &&` and every flag-off row fails. Neither
// half is decoration.
func TestPortalLinkDisclosureAllowed_TruthTable(t *testing.T) {
	cases := []discloseCase{
		{
			name: "stock install discloses nothing",
			want: false,
			why: "THE DEFECT THIS PHASE EXISTS FOR. Nothing set at all: APP_ENV " +
				"defaults to production and the flag defaults to false, so the two " +
				"controls agree. Under the rule this replaced the same row " +
				"disclosed, because 'link' and 'development' were also both defaults.",
		},
		{
			name: "unset environment refuses an explicit flag",
			flag: "true",
			want: false,
			why:  "the APP_ENV default is production, so the flag loses even unset",
		},
		{
			name:   "production refuses the flag",
			appEnv: "production",
			flag:   "true",
			want:   false,
			why: "'prod never discloses regardless of the flag' — the row that makes " +
				"a leaked development .env survivable on a production host",
		},
		{name: "production, flag off", appEnv: "production", flag: "false", want: false},
		{name: "production, flag unset", appEnv: "production", want: false},
		{
			name:   "development without the flag",
			appEnv: "development",
			want:   false,
			why: "the row the old rule got wrong: delivery defaulted to 'link' and " +
				"this disclosed. Disclosure is now affirmative or it does not happen.",
		},
		{
			name:   "development with the flag",
			appEnv: "development",
			flag:   "true",
			want:   true,
			why:    "the affordance is real and must survive — a developer has no mailbox",
		},
		{name: "development, flag off", appEnv: "development", flag: "false", want: false},
		{
			name:   "test with the flag",
			appEnv: "test",
			flag:   "true",
			want:   true,
			why: "what web/playwright.config.ts relies on. If this row goes false the " +
				"portal E2E suite cannot sign anyone in.",
		},
		{name: "test without the flag", appEnv: "test", want: false},
		{
			name:   "an unrecognised environment is not production",
			appEnv: "staging",
			flag:   "true",
			want:   true,
			why: "IsProduction() is an equality test, so 'staging' is non-production " +
				"and the flag governs. Stated rather than left to be discovered: an " +
				"operator on a staging name gets exactly what they asked for.",
		},
		{name: "an unrecognised environment still needs the flag", appEnv: "staging", want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := tc.load(t)
			if got := cfg.PortalLinkDisclosureAllowed(); got != tc.want {
				t.Errorf("APP_ENV=%q AZIMUTHAL_PORTAL_DISCLOSE_LINK=%q: disclosure=%v, want %v\n%s",
					tc.appEnv, tc.flag, got, tc.want, tc.why)
			}
		})
	}
}

// TestPortalLinkDisclosureAllowed_ProductionWinsOverEveryFlagSpelling closes the
// gap a truth table with one spelling leaves. viper's GetBool accepts several
// forms of true, and a rule that refused only the literal "true" would be a rule
// that a "1" walked straight past.
func TestPortalLinkDisclosureAllowed_ProductionWinsOverEveryFlagSpelling(t *testing.T) {
	for _, spelling := range []string{"true", "TRUE", "True", "1", "t"} {
		t.Run(spelling, func(t *testing.T) {
			cfg := discloseCase{appEnv: "production", flag: spelling}.load(t)

			// The premise: this spelling really did set the flag. Without this
			// the test would pass for a value viper parsed as false, which is
			// the assertion-that-cannot-fail shape.
			if !cfg.PortalDiscloseLink {
				t.Fatalf("AZIMUTHAL_PORTAL_DISCLOSE_LINK=%q did not set the flag, so this "+
					"row proves nothing about production overriding it", spelling)
			}
			if cfg.PortalLinkDisclosureAllowed() {
				t.Errorf("AZIMUTHAL_PORTAL_DISCLOSE_LINK=%q disclosed in production", spelling)
			}
		})
	}
}

// TestPortalLinkDisclosureAllowed_IsIndependentOfDeliveryMode pins the
// separation of concerns that fix (b) is.
//
// Delivery answers "does Azimuthal send the mail". Disclosure answers "does the
// response body carry a credential". Binding them together is what produced the
// defect: a mode chosen for having no SMTP relay silently also turned on an
// authentication bypass. So the mode must now be irrelevant in both directions —
// email delivery with the flag DOES disclose, and link delivery without the flag
// does NOT.
func TestPortalLinkDisclosureAllowed_IsIndependentOfDeliveryMode(t *testing.T) {
	for _, tc := range []struct {
		delivery string
		flag     string
		want     bool
	}{
		{delivery: config.PortalLinkDeliveryLink, flag: "true", want: true},
		{delivery: config.PortalLinkDeliveryLink, flag: "false", want: false},
		{delivery: config.PortalLinkDeliveryEmail, flag: "true", want: true},
		{delivery: config.PortalLinkDeliveryEmail, flag: "false", want: false},
	} {
		t.Run(tc.delivery+"/"+tc.flag, func(t *testing.T) {
			t.Setenv("DATABASE_URL", "postgres://user:pass@localhost:5432/db")
			t.Setenv("APP_ENV", "development")
			t.Setenv("AZIMUTHAL_PORTAL_LINK_DELIVERY", tc.delivery)
			t.Setenv("AZIMUTHAL_PORTAL_DISCLOSE_LINK", tc.flag)
			// Email delivery refuses to boot without an explicit relay, which
			// is a different rule and not the one under test here.
			t.Setenv("SMTP_HOST", "smtp.example.com")

			cfg, err := config.Load()
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := cfg.PortalLinkDisclosureAllowed(); got != tc.want {
				t.Errorf("delivery=%q flag=%q: disclosure=%v, want %v — the delivery mode "+
					"must not influence disclosure in either direction",
					tc.delivery, tc.flag, got, tc.want)
			}
		})
	}
}

// TestLoad_PortalDiscloseLinkDefaultsToOff pins the flag's own default,
// independently of the environment name.
//
// The truth table's stock-install row cannot do this job. With APP_ENV also
// defaulting to production, that row would still read false if the flag
// defaulted to TRUE — production would mask it — so the two controls have to be
// asserted separately or one of them is only apparently safe. The APP_ENV half
// lives in TestLoad_Defaults, beside the other defaults.
func TestLoad_PortalDiscloseLinkDefaultsToOff(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://user:pass@localhost:5432/db")
	t.Setenv("AZIMUTHAL_PORTAL_DISCLOSE_LINK", "")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.PortalDiscloseLink {
		t.Error("AZIMUTHAL_PORTAL_DISCLOSE_LINK must default to false — publishing a " +
			"sign-in credential is not something anybody should get by not deciding")
	}
}
