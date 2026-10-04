package config

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// Telegram configures the Telegram channels (docs/02 §11.5, ADR-0014):
// TOLKOVO_TELEGRAM_*.
type Telegram struct {
	// APIID and APIHash name the Telegram application of the installation
	// (my.telegram.org → API development tools). Personal accounts sign in
	// through it; without them the panel offers bots only.
	APIID   int    `env:"API_ID" envDefault:"0"`
	APIHash Secret `env:"API_HASH"`
	// BotAPIURL is the Bot API base, e.g. a local telegram-bot-api server
	// when api.telegram.org is blocked (docs/06 R2).
	BotAPIURL string `env:"BOT_API_URL" envDefault:"https://api.telegram.org"`
}

// apiHashRE is the shape of an api_hash from my.telegram.org.
var apiHashRE = regexp.MustCompile(`^[0-9a-f]{32}$`)

// TelegramUserEnabled reports whether personal Telegram accounts can be
// connected.
func (c Config) TelegramUserEnabled() bool {
	return c.Telegram.APIID != 0 && c.Telegram.APIHash != ""
}

func (c Config) validateTelegram() []error {
	t, p := c.Telegram, Prefix+"TELEGRAM_"
	var errs []error
	if (t.APIID != 0) != (t.APIHash != "") {
		errs = append(errs, fmt.Errorf("%sAPI_ID and %sAPI_HASH are set together (my.telegram.org), or both left empty", p, p))
	}
	if t.APIID < 0 {
		errs = append(errs, fmt.Errorf("%sAPI_ID must be a positive number", p))
	}
	if t.APIHash != "" && !apiHashRE.MatchString(strings.TrimSpace(t.APIHash.Reveal())) {
		errs = append(errs, fmt.Errorf("%sAPI_HASH must be 32 hex characters", p))
	}
	if u, err := url.Parse(t.BotAPIURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" ||
		u.RawQuery != "" || u.Fragment != "" {
		errs = append(errs, fmt.Errorf("%sBOT_API_URL must be an absolute http(s) URL, got %q", p, t.BotAPIURL))
	}
	return errs
}
