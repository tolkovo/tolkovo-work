package github

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	tcrypto "github.com/tolkovo/tolkovo/apps/server/internal/platform/crypto"
)

func testKey(t *testing.T) (*rsa.PrivateKey, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return key, string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))
}

// verifyJWT checks the App JWT the way GitHub does: RS256 by the App's
// public key, iss is the App id, the token is valid now.
func verifyJWT(t *testing.T, header string, pub *rsa.PublicKey, appID string, now time.Time) {
	t.Helper()
	tok, ok := strings.CutPrefix(header, "Bearer ")
	parts := strings.Split(tok, ".")
	if !ok || len(parts) != 3 {
		t.Fatalf("authorization: %q", header)
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, sum[:], sig); err != nil {
		t.Fatalf("signature: %v", err)
	}
	raw, _ := base64.RawURLEncoding.DecodeString(parts[1])
	var claims struct {
		Iat int64  `json:"iat"`
		Exp int64  `json:"exp"`
		Iss string `json:"iss"`
	}
	if err := json.Unmarshal(raw, &claims); err != nil {
		t.Fatal(err)
	}
	if claims.Iss != appID || claims.Iat > now.Unix() || claims.Exp <= now.Unix() || claims.Exp-claims.Iat > 600 {
		t.Fatalf("claims: %+v", claims)
	}
}

func TestClient(t *testing.T) {
	key, keyPEM := testKey(t)
	now := time.Now()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /app-manifests/good/conversions", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" || r.Header.Get("Accept") != "application/vnd.github+json" {
			t.Errorf("manifest headers: %v", r.Header)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": 42, "slug": "tolkovo-zerno", "html_url": "https://github.com/apps/tolkovo-zerno",
			"owner": map[string]any{"login": "zerno"}, "pem": keyPEM, "webhook_secret": "whsec", "client_secret": "never stored",
		})
	})
	mux.HandleFunc("GET /app/installations/7", func(w http.ResponseWriter, r *http.Request) {
		verifyJWT(t, r.Header.Get("Authorization"), &key.PublicKey, "42", now)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 7, "app_id": 42, "account": map[string]any{"login": "zerno"}})
	})
	mux.HandleFunc("GET /app/installations/8", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 8, "app_id": 99, "account": map[string]any{"login": "other"}})
	})
	mux.HandleFunc("POST /app/installations/7/access_tokens", func(w http.ResponseWriter, r *http.Request) {
		verifyJWT(t, r.Header.Get("Authorization"), &key.PublicKey, "42", now)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"token": "ghs_x", "expires_at": now.Add(time.Hour)})
	})
	mux.HandleFunc("GET /installation/repositories", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "token ghs_x" || r.URL.Query().Get("per_page") != "100" {
			t.Errorf("repositories request: %v %v", r.Header, r.URL)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"total_count": 101, "repositories": []map[string]any{
			{"id": 1, "full_name": "zerno/desktop", "private": true, "html_url": "https://github.com/zerno/desktop"},
		}})
	})
	mux.HandleFunc("GET /repos/zerno/desktop", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 1, "full_name": "zerno/desktop", "private": true})
	})
	mux.HandleFunc("GET /repos/zerno/broken", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	})
	mux.HandleFunc("GET /repos/zerno/denied", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	c := New(srv.URL, "https://github.com/", srv.Client())
	c.now = func() time.Time { return now }
	ctx := context.Background()

	if got := c.NewAppURL("", "s t"); got != "https://github.com/settings/apps/new?state=s+t" {
		t.Errorf("NewAppURL: %s", got)
	}
	if got := c.NewAppURL("zerno-team", "s"); got != "https://github.com/organizations/zerno-team/settings/apps/new?state=s" {
		t.Errorf("NewAppURL org: %s", got)
	}
	if got := c.InstallURL("tolkovo-zerno"); got != "https://github.com/apps/tolkovo-zerno/installations/new" {
		t.Errorf("InstallURL: %s", got)
	}

	creds, err := c.ConvertManifest(ctx, "good")
	if err != nil {
		t.Fatalf("ConvertManifest: %v", err)
	}
	if creds.ID != 42 || creds.Slug != "tolkovo-zerno" || creds.Owner != "zerno" || creds.PrivateKey.Reveal() != keyPEM ||
		creds.WebhookSecret.Reveal() != "whsec" {
		t.Fatalf("credentials: %+v", creds)
	}
	if _, err := c.ConvertManifest(ctx, "used"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("used code: %v", err)
	}

	app := App{ID: 42, PrivateKey: creds.PrivateKey}
	inst, err := c.Installation(ctx, app, 7)
	if err != nil || inst.ID != 7 || inst.Account != "zerno" {
		t.Fatalf("Installation: %+v %v", inst, err)
	}
	if _, err := c.Installation(ctx, app, 8); !errors.Is(err, ErrNotFound) {
		t.Fatalf("installation of another App: %v", err)
	}
	if _, err := c.Installation(ctx, App{ID: 42, PrivateKey: tcrypto.NewSecret("not a key")}, 7); !errors.Is(err, ErrPrivateKey) {
		t.Fatalf("bad key: %v", err)
	}

	token, err := c.InstallationToken(ctx, app, 7)
	if err != nil || token.Reveal() != "ghs_x" {
		t.Fatalf("InstallationToken: %v", err)
	}
	repos, more, err := c.Repositories(ctx, token)
	if err != nil || !more || len(repos) != 1 || repos[0].FullName != "zerno/desktop" || !repos[0].Private {
		t.Fatalf("Repositories: %+v %v %v", repos, more, err)
	}
	if r, err := c.Repository(ctx, token, "zerno/desktop"); err != nil || r.ID != 1 {
		t.Fatalf("Repository: %+v %v", r, err)
	}
	for name, want := range map[string]error{
		"zerno/missing": ErrNotFound, "zerno/broken": ErrUnavailable, "zerno/denied": ErrUnauthorized,
		"no-slash": ErrNotFound, "a/b/c": ErrNotFound,
	} {
		if _, err := c.Repository(ctx, token, name); !errors.Is(err, want) {
			t.Errorf("Repository(%s): %v, want %v", name, err, want)
		}
	}
	// Errors never carry the token.
	if _, err := c.Repository(ctx, token, "zerno/broken"); strings.Contains(err.Error(), "ghs_x") {
		t.Fatalf("token in error: %v", err)
	}
}
