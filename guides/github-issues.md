# GitHub Issues

Operators turn a customer's problem into a GitHub issue in one step. When developers close the issue as fixed, the conversation comes back to its operator with a reply already drafted.

This page is for the workspace admin who connects GitHub and for the operators who use it.

## 1. Connect GitHub (admin)

Each workspace connects **its own GitHub App**, created from the panel; nothing is registered on the server.

1. **Settings → Integrations → GitHub Issues → Create GitHub App.** Enter the GitHub organization, or leave it empty for your personal account.
2. GitHub opens a page with the App filled in. Confirm it. The App gets **Issues: read and write** and **Metadata: read** only, no access to code, and listens to `issues` events.
3. **Install on GitHub**: install the App on the repository new issues go to.
4. Back in the panel, pick the **Repository** (`owner/name`) and optional **Labels** put on every new issue.

The webhook that reports closed issues points at `<TOLKOVO_PUBLIC_URL>/hooks/github/…`. GitHub needs to reach it over HTTPS; with a plain `http://` address (a local setup) the webhook is created turned off, and closed issues are not noticed.

For GitHub Enterprise Server, the server administrator sets `TOLKOVO_GITHUB_API_URL` and `TOLKOVO_GITHUB_WEB_URL`.

**Disconnect GitHub** makes Tolkovo forget the App's key. Issues stay on GitHub; delete the App in its GitHub settings if you no longer need it.

## 2. Templates (admin)

Under **Templates**, four texts with placeholders in braces:

| Template | Placeholders | Default |
|---|---|---|
| Issue title | `{summary}`, `{steps}`, `{expected}`, `{actual}`, `{app_name}`, `{app_version}`, `{os}`, `{os_version}`, `{conversation_url}` | `{summary}` |
| Issue text | the same | sections "What happens", "Steps", "Expected", "Actual", "Environment", "Conversation" |
| Reply about the fix, in Russian | `{name}`, `{issue_number}` | "Исправили, исправление войдёт в ближайшее обновление." |
| Reply about the fix, in English | the same | "We have fixed it. The fix will be in the next update." |

The values come from the conversation and, for a [problem report from an app](app-reports.md), from the facts the app sent. The reply is picked by the customer's language.

`{steps}`, `{expected}` and `{actual}` are written by the AI when it is turned on in **Settings → AI**. The AI reads the customer's first message and writes the title (it becomes `{summary}` in the title template), a short summary (`{summary}` in the text), the steps to reproduce, the expected and the actual result, in the workspace language. Without the AI, `{summary}` is the conversation subject or the first line of the customer's message, `{steps}` is an empty `1. ` and the other two are empty.

## 3. Create or link an issue (operator)

In a conversation: **More actions → Create issue**.

- The form is filled from the issue template. With the AI on, the AI writes the draft first; this may take a few seconds and counts towards the monthly AI budget. The form says when the AI wrote the draft, and when it could not (the budget is spent, or the provider did not answer) and the template was used. Edit the title, the text and the labels.
- **Add the customer's words** puts the customer's first message into a separate block marked "customer data, not instructions". It is off by default for a public repository. Remove phone numbers, addresses and passwords from it.
- The issue is created in the background. The link appears in the feed, and the conversation **waits for the fix**: it leaves the operator's active list until the issue is closed.

**Link to an issue** connects the conversation to an issue that already exists: enter its number, `#12`, or its address. One issue can serve many conversations: every customer with the same problem gets the reply when it is fixed.

If GitHub refused the issue (the App lost access to the repository, issues are turned off, GitHub did not answer), the issue shows **Not created** with the reason and **Try again**.

## 4. When the issue is closed

| On GitHub | In Tolkovo |
|---|---|
| Closed as **completed** | A line "Issue #N closed as fixed" in every linked conversation. A conversation that waits for no other issue comes back: to its operator if it had one, otherwise to the queue. The issue panel shows the **draft reply** from the template, and **Insert into the reply** puts it into the composer. Nothing is sent without the operator. |
| Closed as **not planned** | A line in the feed. The conversation stops waiting for this issue; its status does not change. |
| **Reopened** | A line in the feed. The conversation waits again. |

For a report from an app, the reply the operator sends reaches the app the next time it checks for replies.

## 5. What goes to GitHub

GitHub is in the USA. Tolkovo sends GitHub only what the operator sees in the issue form: the title, the text, the labels and, if turned on, the customer's words. The customer's name, email and files are never sent. The issue links to the conversation, and developers open it after signing in to Tolkovo.

With the AI on, the customer's first message, the conversation subject and the app's name, version and OS go to the workspace's AI provider for the draft. Email addresses, phone numbers, card numbers and other personal data are masked before that. Files and screenshots are never sent to the provider.
