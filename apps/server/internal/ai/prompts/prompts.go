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

// IssueDraft is the system prompt of the issue draft from a user's report
// (docs/03 §8.1).
//
//go:embed issue_draft.txt
var IssueDraft string

// IssueDraftSchema is the JSON Schema of the issue draft's answer.
//
//go:embed issue_draft.schema.json
var IssueDraftSchema []byte

// BotReply is the system prompt of the AI bot's turn (ADR-0018); the
// workspace's company description and rules follow it.
//
//go:embed bot_reply.txt
var BotReply string

// BotReplySchema is the JSON Schema of the bot's answer.
//
//go:embed bot_reply.schema.json
var BotReplySchema []byte
