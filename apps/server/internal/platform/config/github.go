package config

import (
	"fmt"
	"net/url"
)

// GitHub configures the GitHub integration (GH-01, ADR-0012):
// TOLKOVO_GITHUB_*. The defaults are github.com; GitHub Enterprise Server
// has its own addresses.
type GitHub struct {
	// APIURL is the REST API base, e.g. https://ghe.example.com/api/v3.
	APIURL string `env:"API_URL" envDefault:"https://api.github.com"`
	// WebURL is where the browser creates and installs the App, e.g.
	// https://ghe.example.com.
	WebURL string `env:"WEB_URL" envDefault:"https://github.com"`
}

func (c Config) validateGitHub() []error {
	g, p := c.GitHub, Prefix+"GITHUB_"
	var errs []error
	for _, f := range []struct{ name, value string }{{"API_URL", g.APIURL}, {"WEB_URL", g.WebURL}} {
		if u, err := url.Parse(f.value); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" ||
			u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			errs = append(errs, fmt.Errorf("%s%s must be an absolute http(s) URL, got %q", p, f.name, f.value))
		}
	}
	return errs
}
