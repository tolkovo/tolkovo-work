package github

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

// Webhooks of the App (docs.github.com, "Webhook events and payloads").

// VerifySignature reports whether header (X-Hub-Signature-256,
// "sha256=<hex>") is the HMAC-SHA256 of body under the App's webhook
// secret. The comparison takes the same time whatever the header holds.
func VerifySignature(secret []byte, header string, body []byte) bool {
	hexSum, ok := strings.CutPrefix(header, "sha256=")
	if !ok || len(secret) == 0 {
		return false
	}
	got, err := hex.DecodeString(hexSum)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return hmac.Equal(got, mac.Sum(nil))
}

// Sign returns the signature header of body under secret (tests and fakes).
func Sign(secret []byte, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// HookEvent is the part of the "issues" and "installation" events the
// integration reads: ids, numbers and states, never titles or texts.
//
// The "issues" event does not say which pull request closed the issue:
// GitHub has that only in the issue's timeline, behind another API call.
type HookEvent struct {
	Action string `json:"action"`
	Issue  *struct {
		Number int64  `json:"number"`
		State  string `json:"state"`
		// StateReason of a closed issue: completed, not_planned, duplicate;
		// null from older clients, which close as completed.
		StateReason *string   `json:"state_reason"`
		PullRequest *struct{} `json:"pull_request,omitempty"`
	} `json:"issue"`
	Repository *struct {
		ID int64 `json:"id"`
	} `json:"repository"`
	Installation *struct {
		ID int64 `json:"id"`
	} `json:"installation"`
}

// ParseHookEvent decodes a webhook body.
func ParseHookEvent(body []byte) (HookEvent, error) {
	var ev HookEvent
	if err := json.Unmarshal(body, &ev); err != nil {
		return HookEvent{}, fmt.Errorf("github: decode webhook: %w", err)
	}
	return ev, nil
}
