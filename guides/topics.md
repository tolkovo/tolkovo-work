# Topics

A **topic** says what a question is about: one product, one kind of request. The customer picks it before writing: the Telegram bot shows a button per topic, and the website widget shows them above the message field. The team sees the topic on every conversation in the inbox, filters by it, and knows what to take first.

This page is for the workspace admin who sets topics up and for operators who work with them.

## 1. Who does what

- **Owners and admins** create, change, reorder and delete topics: they decide who gets a topic's conversations and how soon.
- **Every operator** sees topics in the inbox, filters by them and changes the topic of any conversation they can see.

## 2. Settings (admin)

**Settings → Topics** holds up to 20 topics. Each topic has:

- **Name.** The customer sees it on the button, so keep it short: "CRM", "OnlyFans", "Fansly", "Other".
- **Color.** It marks the topic in the inbox.
- **Order.** The arrows move a topic up or down. The bot and the widget show topics in this order.
- **Priority:** normal, raised, high or urgent. With automatic distribution, higher-priority conversations leave the queue first.
- **Who gets its conversations.** With **Settings → Distribution → In turn**, only the checked members get the topic's conversations. If nobody is checked, everyone does. Anyone can still take a conversation from the queue by hand.
- **Task queue.** A task created from a conversation of this topic goes to this queue unless another queue is picked.

Without topics, the bot and the widget ask nothing.

## 3. What the customer sees

- **Telegram bot.** After the first message of a new conversation, the bot sends "Choose the topic of your question" with a button per topic. A customer who has just opened the bot and pressed Start sees the buttons before writing. Pressing a button sets the topic, and the buttons turn into "Topic: CRM". The bot asks once per conversation. A customer who writes without pressing a button still reaches the team: the conversation shows "No topic" until someone picks one.
- **Website widget.** The topics sit above the message field. A topic picked before the first message goes with it. In an open conversation without a topic, picking one sets it at once.
- **Personal Telegram account.** Telegram does not let personal accounts send buttons, so an operator sets the topic there.

## 4. In the inbox (operator)

- **The list.** Every row shows its topic. The chips above the list filter it: "All topics", each topic, or "No topic". The tabs keep the filter.
- **A conversation.** **Topic of the question**, next to the tags, changes the topic. The conversation takes the topic's priority and stays with whoever has it.
- **The feed** shows a line for each change: "The customer chose the topic «CRM»", "Topic: CRM · Anna", "Topic removed".

When a customer picks a topic in a conversation that automatic distribution already gave to someone outside that topic's members, and nobody has replied yet, the conversation goes back to the queue for the topic's members.

## 5. Tasks

- A task created from a conversation takes the conversation's topic. If the task has no queue, it takes the topic's queue.
- The task list filters by topic. A task's topic can be changed on the task, and the feed records each change.
- Over the API, `GET /api/v1/tasks?topic=<id>|none` filters tasks, and `PUT /api/v1/tasks/{id}` takes `topic_id`. AI agents see each task's topic through MCP.

## 6. API

- `GET /api/v1/topics` lists the topics. It needs access to conversations or tasks; an access token with `tasks:read` works too.
- `POST /api/v1/topics`, `PUT /api/v1/topics/{id}`, `DELETE /api/v1/topics/{id}` and `PUT /api/v1/topics/order` change them. They need the "routing" permission (owner, admin).
- `PUT /api/v1/conversations/{id}/topic` with `{"topic_id": "<id>"}` or `null` sets or removes a conversation's topic.
- `GET /api/v1/conversations?topic=<id>|none` filters the inbox.

The reference is [api/openapi.yaml](../api/openapi.yaml).
