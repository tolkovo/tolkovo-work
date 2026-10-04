// Shape of the landing copy. RU and EN must implement it fully, so a missing
// key fails `astro check` instead of shipping an untranslated page (00 §7.5).

export type Locale = "ru" | "en";

/** Text with inline code fragments, e.g. ["Self-host with ", { code: "docker compose up" }, "."] */
export type Rich = ReadonlyArray<string | { code: string }>;

export type ChannelStatus = "on" | "soon" | "plan";

export interface BoardEvent {
  /** Input jack id: web | tg | biz */
  from: "web" | "tg" | "biz";
  who: string;
  /** "ai" when the AI closed it, otherwise the line id */
  to: "ai" | "L1" | "L2" | "L3";
  agent?: string;
  note: string;
}

export interface Dict {
  meta: {
    lang: string;
    ogLocale: string;
    title: string;
    description: string;
    ogImageAlt: string;
  };
  header: {
    skip: string;
    navLabel: string;
    nav: ReadonlyArray<{ href: string; label: string }>;
    langLabel: string;
    github: string;
    cta: string;
    homeLabel: string;
  };
  hero: {
    eyebrow: string;
    h1: string;
    h1Accent: string;
    lede: string;
    ctaSelf: string;
    ctaGithub: string;
    trust: string;
  };
  board: {
    label: string;
    eyebrow: string;
    live: string;
    colIn: string;
    colAi: string;
    colLines: string;
    jacks: ReadonlyArray<{ id: "web" | "tg" | "biz"; name: string; sub: string }>;
    aiTitle: string;
    aiText: string;
    kAiLabel: string;
    kHLabel: string;
    lines: ReadonlyArray<{
      id: "L1" | "L2" | "L3";
      name: string;
      agents: ReadonlyArray<{ name: string; load: number; cap: number }>;
    }>;
    ticker: ReadonlyArray<{ time: string; kind: "a" | "h"; text: string }>;
    events: ReadonlyArray<BoardEvent>;
    channelShort: { web: string; tg: string; biz: string };
    note: string;
  };
  why: ReadonlyArray<{ mark: string; title: string; text: Rich }>;
  features: {
    eyebrow: string;
    title: string;
    rows: ReadonlyArray<{
      title: string;
      who: string;
      feats: ReadonlyArray<{ title: string; text: Rich; badge?: string }>;
    }>;
  };
  handoff: {
    eyebrow: string;
    title: string;
    lede: string;
    chatLabel: string;
    chatHead: string;
    chatTime: string;
    messages: ReadonlyArray<
      | { kind: "client" | "ai" | "operator"; who: string; text: string }
      | { kind: "system"; text: string }
    >;
    steps: ReadonlyArray<{ title: string; text: string }>;
    summaryEyebrow: string;
    summaryText: string;
    summaryFacts: ReadonlyArray<{ term: string; value: string; mono?: boolean }>;
  };
  channels: {
    eyebrow: string;
    title: string;
    items: ReadonlyArray<{ name: string; status: ChannelStatus; label: string }>;
    footBefore: string;
    footLink: string;
    footAfter: string;
  };
  compare: {
    eyebrow: string;
    title: string;
    head: readonly [string, string, string, string];
    rows: ReadonlyArray<{
      label: string;
      /** Tolkovo, Jivo, Chatwoot, Intercom; "yes"/"no" marks a styled cell */
      cells: ReadonlyArray<{ text: string; tone?: "yes" | "no" }>;
    }>;
    note: string;
  };
  selfhost: {
    eyebrow: string;
    title: string;
    text: string;
    checks: ReadonlyArray<string>;
    termLabel: string;
    commentQuick: string;
    commentManual: string;
    commentOpen: string;
    copy: string;
    copied: string;
    selected: string;
  };
  oss: {
    eyebrow: string;
    title: string;
    text: string;
    issues: string;
    chat: string;
    licenses: ReadonlyArray<{ title: string; text: string }>;
  };
  faq: {
    eyebrow: string;
    title: string;
    items: ReadonlyArray<{ q: string; a: string }>;
  };
  /** Closing block: where to start now that there is no cloud waitlist */
  start: {
    title: string;
    text: string;
    ctaSelf: string;
    ctaReleases: string;
  };
  footer: {
    cols: ReadonlyArray<{ title: string; links: ReadonlyArray<{ label: string; href: string }> }>;
    bottom: string;
  };
}
