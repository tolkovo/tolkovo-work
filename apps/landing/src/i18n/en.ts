// EN copy: a full version of the RU page, not a literal translation (docs/05-landing.md).
import { COMMUNITY_CHAT_URL, REPO_URL } from "../site";
import type { Dict } from "./types";

export const en: Dict = {
  meta: {
    lang: "en",
    ogLocale: "en_US",
    title: "Tolkovo — open-source live chat with an AI agent and Telegram",
    description:
      "Your website widget, Telegram bot and Telegram Business in one shared team inbox. The AI answers from your knowledge base and hands tricky conversations to the right support line. Open source, in the cloud or on your own server.",
    ogImageAlt: "Tolkovo: every chat in one window. AI answers, people decide.",
  },
  header: {
    skip: "Skip to content",
    navLabel: "Sections",
    nav: [
      { href: "#features", label: "Features" },
      { href: "#channels", label: "Channels" },
      { href: "#selfhost", label: "Self-host" },
      { href: "#oss", label: "Open source" },
      { href: "#faq", label: "FAQ" },
    ],
    langLabel: "Language",
    github: "GitHub",
    cta: "Self-host it",
    homeLabel: "Tolkovo, home",
  },
  hero: {
    eyebrow: "Open-source live chat · website and Telegram",
    h1: "Every chat in one window. AI answers,",
    h1Accent: "people decide.",
    lede:
      "Your website widget, Telegram bot and Telegram Business land in one shared team inbox. The AI handles routine questions from your knowledge base and passes the hard ones to an operator on the right line, with a short summary of the conversation. In the cloud or on your own server.",
    ctaSelf: "Self-host it",
    ctaGithub: "Code on GitHub →",
    trust: "AGPL-3.0 · Go + React · Docker · RU / EN · No trackers on this site",
  },
  board: {
    label: "Demo: how conversations flow through the AI to operators",
    eyebrow: "Shift switchboard",
    live: "demo · 14:32 CET",
    colIn: "Where customers write",
    colAi: "First line",
    colLines: "Lines and operators on shift",
    jacks: [
      { id: "web", name: "Website", sub: "widget" },
      { id: "tg", name: "Telegram bot", sub: "@shop_support_bot" },
      { id: "biz", name: "Telegram Business", sub: "@shop_manager" },
    ],
    aiTitle: "AI agent",
    aiText: "Answers from the knowledge base. Unsure, asked for a human or the topic is on the list — it calls an operator.",
    kAiLabel: "resolved alone",
    kHLabel: "handed to people",
    lines: [
      {
        id: "L1",
        name: "Support",
        agents: [
          { name: "Anna", load: 2, cap: 4 },
          { name: "Peter", load: 3, cap: 4 },
        ],
      },
      { id: "L2", name: "Billing and invoices", agents: [{ name: "Oleg", load: 1, cap: 3 }] },
      { id: "L3", name: "Technical", agents: [{ name: "Nika", load: 0, cap: 2 }] },
    ],
    ticker: [
      { time: "14:29", kind: "a", text: "Guest #4817 · website → AI answered from “Returns”" },
      { time: "14:30", kind: "h", text: "Alina R. · Telegram → L1 · Anna · customer asked for an operator" },
      { time: "14:31", kind: "a", text: "Guest #4822 · website → AI answered from “Payment methods”" },
    ],
    events: [
      { from: "web", who: "Guest #4821", to: "ai", note: "AI answered from “Delivery times”" },
      { from: "tg", who: "Ivan Petrov", to: "L1", agent: "Anna", note: "customer asked for an operator" },
      { from: "biz", who: "Acme Ltd", to: "L2", agent: "Oleg", note: "topic “closing documents”" },
      { from: "web", who: "Guest #4830", to: "ai", note: "AI answered from “Changing your email”" },
      { from: "tg", who: "Maria K.", to: "L1", agent: "Peter", note: "ready to buy, wants a discount" },
      { from: "tg", who: "Sergey V.", to: "L3", agent: "Nika", note: "API error, AI found no answer" },
    ],
    channelShort: { web: "website", tg: "Telegram", biz: "TG Business" },
    note: "Example with made-up customers and numbers.",
  },
  why: [
    {
      mark: "Telegram, no middlemen",
      title: "Your customers already use Telegram",
      text: [
        "Don’t make them hunt for a widget. Your company bot and Telegram Business account arrive in the same window as your website chat. Customers write to you personally, your team replies.",
      ],
    },
    {
      mark: "AI that knows its limits",
      title: "AI takes the routine",
      text: [
        "The agent answers from your knowledge base around the clock. When it is unsure, the customer is upset or asks for a person, it hands the conversation to an operator instead of looping.",
      ],
    },
    {
      mark: "Your server, your keys",
      title: "Your data stays yours",
      text: [
        "Self-host with a single ",
        { code: "docker compose up" },
        ". Connect Claude, OpenAI or a local model via Ollama, and conversations never leave your server.",
      ],
    },
  ],
  features: {
    eyebrow: "Features",
    title: "The right tool for everyone on the team",
    rows: [
      {
        title: "For operators",
        who: "in the inbox all day",
        feats: [
          { title: "One inbox", text: ["Website and Telegram in a single feed: assignment, notes for colleagues, tags, canned replies via “/”."] },
          { title: "AI suggestions", text: ["A draft reply and a summary of long conversations. Only you hit send."] },
          { title: "Keyboard first", text: ["Take, transfer, close and jump to the next chat without touching the mouse."] },
          { title: "Customer profile", text: ["Past conversations, the page they are on, their local time and language."] },
        ],
      },
      {
        title: "For team leads",
        who: "own the shift and the SLA",
        feats: [
          { title: "L1 / L2 / L3 lines", text: ["One-click escalation with a reason. The second line sees the first line’s history and notes."] },
          { title: "Shifts and routing", text: ["Round-robin or least-loaded, chat limits per operator, skills and languages."] },
          {
            title: "Real-time monitoring",
            text: ["Who has waited longest, who is overloaded, where the SLA is burning. Reassign, whisper, take over."],
          },
          { title: "Roles and access", text: ["A second-line operator doesn’t see other queues, an analyst can’t message customers."] },
        ],
      },
      {
        title: "For developers",
        who: "deploy and embed it",
        feats: [
          {
            title: "Go + React, one binary",
            text: ["Postgres, Redis, S3. Upgrade with ", { code: "docker compose pull" }, "."],
          },
          { title: "Any model, your keys", text: ["Claude, OpenAI, Ollama, vLLM. No markup, no middlemen."] },
          { title: "MIT-licensed widget", text: ["About 35 KB, Shadow DOM, a JS API and HMAC user verification."] },
          {
            title: "API and webhooks",
            text: ["OpenAPI 3.1, conversation events, custom channels via the Channel API."],
            badge: "by v1.0",
          },
        ],
      },
    ],
  },
  handoff: {
    eyebrow: "Human handoff",
    title: "AI answers. A human steps in on time",
    lede: "Customers can always reach a person, and they never have to repeat themselves.",
    chatLabel: "Example conversation",
    chatHead: "Ivan Petrov · Telegram bot",
    chatTime: "14:33",
    messages: [
      { kind: "client", who: "Ivan", text: "I’m not getting the login confirmation code" },
      {
        kind: "ai",
        who: "AI agent",
        text: "Please check your spam folder and that your number starts with the country code. The code is valid for 5 minutes, and you can request a new one after 60 seconds.",
      },
      { kind: "client", who: "Ivan", text: "Already did. Can I talk to a person, please?" },
      { kind: "ai", who: "AI agent", text: "Passing you to a specialist. We usually reply within 2–3 minutes." },
      { kind: "system", text: "handed to L1 · reason: customer asked for an operator" },
      {
        kind: "operator",
        who: "Anna · L1",
        text: "Ivan, I can see your carrier is blocking our SMS. I’ve sent the code to this chat, it will arrive in a minute.",
      },
    ],
    steps: [
      {
        title: "AI takes the conversation first",
        text: "It answers routine questions instantly and only from your own content. An answer without a source is never sent.",
      },
      {
        title: "A handoff rule fires",
        text: "Low model confidence, a request for a human, frustration, topics like “payment” or “refund”. You set the rules.",
      },
      {
        title: "The operator gets the context",
        text: "A free operator on the right line sees the summary, the full history and a draft reply.",
      },
    ],
    summaryEyebrow: "What Anna sees on handoff",
    summaryText:
      "Customer doesn’t receive the SMS login code. The AI suggested checking spam and the number format, it didn’t help. Customer is calm but asked for a person.",
    summaryFacts: [
      { term: "Channel", value: "Telegram bot, customer since March" },
      { term: "History", value: "2 conversations, rated 5" },
      { term: "Waiting", value: "00:41", mono: true },
    ],
  },
  channels: {
    eyebrow: "Channels",
    title: "Reply where your customers write",
    items: [
      { name: "Website widget", status: "on", label: "in MVP" },
      { name: "Telegram bot", status: "on", label: "in MVP" },
      { name: "Telegram Business", status: "on", label: "in MVP" },
      { name: "API and webhooks", status: "soon", label: "by v1.0" },
      { name: "Email", status: "plan", label: "after v1.0" },
      { name: "WhatsApp", status: "plan", label: "after v1.0" },
      { name: "VK / MAX", status: "plan", label: "after v1.0" },
    ],
    footBefore: "Missing a channel? ",
    footLink: "Vote on the roadmap",
    footAfter: " or write a connector.",
  },
  compare: {
    eyebrow: "Comparison",
    title: "How Tolkovo is different",
    head: ["Tolkovo", "Jivo", "Chatwoot", "Intercom"],
    rows: [
      {
        label: "Open source",
        cells: [
          { text: "yes", tone: "yes" },
          { text: "no", tone: "no" },
          { text: "yes", tone: "yes" },
          { text: "no", tone: "no" },
        ],
      },
      {
        label: "Self-hosted",
        cells: [
          { text: "docker compose", tone: "yes" },
          { text: "no", tone: "no" },
          { text: "yes", tone: "yes" },
          { text: "no", tone: "no" },
        ],
      },
      {
        label: "Telegram Business",
        cells: [{ text: "yes", tone: "yes" }, { text: "to be verified" }, { text: "to be verified" }, { text: "no", tone: "no" }],
      },
      {
        label: "Choice of model, your own keys",
        cells: [
          { text: "including local", tone: "yes" },
          { text: "no", tone: "no" },
          { text: "partly" },
          { text: "no", tone: "no" },
        ],
      },
      {
        label: "Pricing",
        cells: [{ text: "free" }, { text: "per operator" }, { text: "OSS + paid cloud" }, { text: "subscription + AI fees" }],
      },
      {
        label: "Stack",
        cells: [{ text: "Go + React" }, { text: "closed" }, { text: "Rails + Vue" }, { text: "closed" }],
      },
    ],
    note: "Based on public information. Cells marked “to be verified” are checked before launch. Spotted a mistake? Open an issue.",
  },
  selfhost: {
    eyebrow: "Self-host",
    title: "Your own server in five minutes",
    text: "One compose file: the Go server, the web app, PostgreSQL and Redis. A VPS with 2 vCPU and 4 GB of RAM is enough for a small team.",
    checks: [
      "Telemetry stays off until you turn it on",
      "Local models via Ollama or vLLM",
      "Data lives only in your database. Host it in your own jurisdiction",
    ],
    termLabel: "Install commands",
    commentQuick: "# download and run",
    commentManual: "# or by hand",
    commentOpen: "# open https://your-domain — the setup wizard creates the owner",
    copy: "Copy",
    copied: "Copied",
    selected: "Selected, press ⌘C",
  },
  oss: {
    eyebrow: "Open source",
    title: "Built in the open",
    text: "Code, roadmap and discussions are public. Suggest features, vote for channels, send PRs. Issues labelled good first issue are a good place to start.",
    issues: "Good first issues",
    chat: "Community chat on Telegram →",
    licenses: [
      {
        title: "Core — AGPL-3.0",
        text: "Use, modify and deploy it for free. If you run a modified version as a service for others, publish your changes.",
      },
      { title: "Widget and SDK — MIT", text: "Embed them in any website or app, no strings attached." },
    ],
  },
  faq: {
    eyebrow: "FAQ",
    title: "Frequently asked questions",
    items: [
      {
        q: "Is it really free?",
        a: "Yes. The code is open and self-hosting is free with no limit on operators. The cloud is free at launch too. If paid plans ever appear, self-hosting stays free.",
      },
      {
        q: "Where is cloud data stored?",
        a: "On servers in the EU. If your company has to keep personal data in a specific country, run Tolkovo on your own server there.",
      },
      {
        q: "Which models are supported?",
        a: "Claude, OpenAI and any OpenAI-compatible API, including local Ollama and vLLM. You bring the keys, we add no markup.",
      },
      { q: "Can I use it without AI?", a: "Yes. AI can be switched off completely, and Tolkovo works as a classic live chat." },
      {
        q: "What is Telegram Business?",
        a: "Customers message your personal or business Telegram account, and your team replies from the inbox. It connects through your own bot and requires Telegram Premium.",
      },
      { q: "Will there be WhatsApp, VK and Email?", a: "Yes, after v1.0. The order will be decided by votes on the roadmap." },
    ],
  },
  start: {
    title: "Start on your own server",
    text: "Tolkovo installs with Docker Compose in a few minutes, and your data stays with you. The EU cloud opens later: we will announce it in the Telegram community and in GitHub releases.",
    ctaSelf: "Self-host it",
    ctaReleases: "Watch releases",
  },
  footer: {
    cols: [
      {
        title: "Product",
        links: [
          { label: "Features", href: "#features" },
          { label: "Channels", href: "#channels" },
          { label: "Roadmap", href: `${REPO_URL}/issues` },
          { label: "Changelog", href: `${REPO_URL}/releases` },
        ],
      },
      {
        title: "Developers",
        links: [
          { label: "Self-host", href: "#selfhost" },
          { label: "Code on GitHub", href: REPO_URL },
          { label: "CONTRIBUTING", href: `${REPO_URL}/blob/main/CONTRIBUTING.md` },
        ],
      },
      {
        title: "Community",
        links: [
          { label: "Telegram", href: COMMUNITY_CHAT_URL },
          { label: "Discussions", href: `${REPO_URL}/discussions` },
        ],
      },
      {
        title: "Legal",
        links: [
          { label: "License", href: `${REPO_URL}/blob/main/LICENSE` },
          { label: "Security", href: `${REPO_URL}/blob/main/SECURITY.md` },
        ],
      },
    ],
    bottom: "© 2026 Tolkovo contributors · This site uses no cookies",
  },
};
