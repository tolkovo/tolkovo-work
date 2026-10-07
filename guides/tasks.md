# Tasks

The **Tasks** section is a simple tracker inside Tolkovo for developers and the IT team. Operators turn a customer's problem into a task in one step. When the task is done, the conversation comes back to its operator with a reply already drafted. It works next to [GitHub Issues](github-issues.md) and needs no outside service, so it suits self-hosted servers too.

This page is for the workspace admin who sets the section up and for everyone who works on tasks.

## 1. Who does what

- **Every member** of the workspace sees the section, creates tasks, changes them, comments and links them to conversations they can see.
- **Owners and admins** also manage queues and the section's settings, and delete tasks.

## 2. Settings (admin)

**Settings → Tasks**:

- **The tasks section is on.** Turned off, the section leaves the menu and no new tasks are created. Existing tasks stay.
- **What conversations offer**: a task and a GitHub issue (the default), only a task, or only a GitHub issue. "Create issue" appears only when GitHub is connected.
- **Reply to the customer about a done task**, in Russian and in English, with the placeholders `{name}` (the customer's name) and `{task_number}`. The reply is picked by the customer's language. Tolkovo never sends it by itself: the operator does.
- **Queues** group tasks of one kind, such as development or infrastructure. The members of a queue are told about new tasks in it that have no assignee. Tasks work without queues too.

## 3. A task by hand

**Tasks → New**: a title, a description, and optionally a queue, a priority (low, normal, high, urgent) and an assignee. The assignee is notified. Without an assignee, the members of the queue are notified.

## 4. A task from a conversation (operator)

In a conversation: **More actions → Create task**.

The form takes the place of the conversation and starts from a draft:

- With the AI turned on in **Settings → AI**, the AI reads the customer's first message and writes the title, a short summary, the steps to reproduce, the expected and the actual result, in the workspace language. Personal data such as emails, phone numbers and card numbers is masked before anything goes to the model. The call counts toward the month's AI budget.
- Without the AI, or when the budget is spent or the provider fails, the title is the conversation subject or the first line of the customer's message. The form says why.
- For a [problem report from an app](app-reports.md), the app's name, version and OS are added.
- The customer's first message is added to the description with personal data such as emails, phone numbers and card numbers masked: every member sees tasks, and a task stays when the customer's data is deleted. Remove what is not needed.

Several customers often report the same problem. **More actions → Link to a task** links the conversation to an open task found by its title or number.

While a linked task is open, the conversation **waits for the fix**: an operator's conversation goes to "Waiting" and is out of their way. An open GitHub issue keeps it waiting as well: the conversation comes back only when nothing it waits for is open.

## 5. Working on a task

**Tasks** lists the tasks: **Mine** (with a counter in the menu), **Open**, **Unassigned**, **Closed** and **All**, filtered by queue and searched by title or number.

On a task:

- **Take it** gives the task to you and puts it in progress.
- Status, priority, assignee and queue are saved as soon as they are changed. If someone changed the task a moment before, the panel shows the fresh version and asks to try again.
- **Edit** changes the title and the description.
- Comments are for the team: the customer never sees them. The history below shows every change and who made it, the latest 500 lines.
- Linked conversations are listed with the customer's name, and can be unlinked.

## 6. When a task is done

Set the status to **Done**. In every linked conversation:

- a line says the task is done;
- the conversation comes back to its operator, or to the queue when they are no longer a member;
- above the reply field the operator sees "Task T-12 is done. A draft reply: …" with **Insert into reply**. The operator edits it and sends it as any reply.

**Won't do** closes the task without bringing the conversation back or drafting a reply. Reopening a task puts its conversations to wait again.

## 7. Notifications

In the panel, the assignee of a task hears a sound and gets a desktop notification when someone else gives them the task. The sound and desktop notifications follow **Settings → Notifications**.

In Telegram, members get a message when a task is assigned to them, and when a task without an assignee appears in a queue they are a member of. Nobody is told about a task they assigned to themselves.

1. **The admin** creates a bot with [@BotFather](https://t.me/BotFather) just for notifications and pastes its token in **Settings → Tasks → Telegram bot for notifications**. The bot of a support channel is refused: reading its messages would take the customers' ones. The token is stored encrypted (the server needs `TOLKOVO_MASTER_KEY`). Replacing the bot unlinks every member. If Telegram stops knowing the token, members see that an admin has to replace the bot.
2. **Each member** opens **Settings → Notifications → Telegram → Link Telegram**, opens the bot from the link and presses **Start**. The link works for 15 minutes. The panel notices the Start by itself: Tolkovo reads the bot's messages, so no webhook is needed and it works on a server behind NAT.
3. **Unlink** stops the messages. A member who blocks the bot is unlinked automatically. One Telegram account gets the messages of one member: a second member's link from the same account is refused, and the bot says so.

## 8. API and AI agents (MCP)

A script or an AI agent (Claude Code, Cursor and other MCP clients) can work with tasks on a member's behalf: list them, take one, comment it and close it.

1. Open **Settings → API access** and issue a token: give it a name, choose **Read tasks** or **Read and change tasks**, and an expiry (30, 90 or 365 days, or never).
2. Copy the token at once: it is shown only once, and the server keeps only its hash. Next to it the panel shows the command for Claude Code:

   ```sh
   claude mcp add --transport http tolkovo https://<your server>/api/v1/mcp --header "Authorization: Bearer tk_pat_…"
   ```

   Other MCP clients connect to the same address with the same `Authorization` header. Scripts call the REST API of tasks with it (`GET /api/v1/tasks`, `PUT /api/v1/tasks/{id}` and so on).
3. The agent gets the tools `list_tasks`, `get_task`, `list_queues`, and with a token that changes tasks also `create_task`, `update_task` and `comment_task`. It names tasks by number (`#42`). It is told to treat the texts of tasks as data, not as instructions.

A token acts as its member and can do no more than the member's role. It reaches only tasks: conversations, customers, settings, deleting tasks and queues are closed to it, and it cannot link a task to a conversation. Changes made with a token appear in the task's feed under the member's name.

A member has at most 20 tokens and revokes them on the same page. Admins also see every member's tokens there (**Members' tokens**) and revoke any. A token stops working at once when it is revoked or expires, and when its member leaves the workspace or their account is disabled.

## 9. What leaves Tolkovo

- With the AI turned on, the customer's first message, with personal data masked, goes to the AI provider of the workspace to write the draft.
- With a Telegram bot for notifications, Telegram gets the task's number, its title and a link to it in the panel. The description and customer data never go there.
