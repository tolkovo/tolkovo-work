// Package prompts holds the prompts of the AI features as files (docs/00
// §10.2): a prompt changes in a commit of its own, with the eval results
// of its feature.
package prompts

import _ "embed"

// ConnectionCheck is the system prompt of the connection check in the
// settings: the smallest request that proves the key and the model work.
//
//go:embed connection_check.txt
var ConnectionCheck string
