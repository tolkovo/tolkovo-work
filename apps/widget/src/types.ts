/** Interface language of the widget. "auto" takes <html lang>, then the browser's. */
export type Locale = "ru" | "en";

/** A text in both interface languages, or one text for both. */
export type Localized = string | Partial<Record<Locale, string>>;

/** How the widget looks; everything is optional and set in code (docs/04 §6.3). */
export interface Theme {
  /** Accent color: the launcher, the visitor's messages, the send button. Any CSS color. */
  accent?: string;
  /** Text on the accent; picked for contrast with a hex accent when not given. */
  accentText?: string;
  /** Light, dark, or following the system (default). */
  mode?: "light" | "dark" | "auto";
  /** Corner radius of the window and the messages, px. Default 14. */
  radius?: number;
  /** Corner of the screen. Default bottom-right. */
  position?: "bottom-right" | "bottom-left";
  /** Distance from the screen edges, px. Default 20. */
  offsetX?: number;
  offsetY?: number;
  /** Default 2147483000: above the page. */
  zIndex?: number;
  /** CSS font-family; the system font by default. */
  fontFamily?: string;
}

/** Who the visitor is, as the company's backend knows them (W-04). */
export interface User {
  /** The user's id in the company's system. */
  id: string;
  /** hex HMAC-SHA256 of id under the widget's identity secret, computed on the company's backend. Without it the visitor is unverified. */
  hash?: string;
  name?: string;
  email?: string;
}

/** A field of the pre-chat form. */
export type PrechatField = "name" | "email" | "phone";

/** The form a visitor fills in before the first message (docs/04 §6.3). It is skipped for a visitor the site identified. */
export interface Prechat {
  /** Fields, in this order. Default ["name", "email"]. */
  fields?: PrechatField[];
  /** Fields that must be filled in. Default none. */
  required?: PrechatField[];
  /** A line above the form. */
  intro?: Localized;
  /** A required checkbox of consent to the processing of personal data (152-ФЗ, GDPR), with a link to the policy. */
  consent?: {
    /** An http(s) link to the policy; it is stored with the consent. */
    policyUrl: string;
    /** The checkbox's text; «Даю согласие на обработку персональных данных» by default. */
    text?: Localized;
  };
}

/** A key-value store for the session token; localStorage by default. */
export interface TokenStorage {
  getItem(key: string): string | null;
  setItem(key: string, value: string): void;
  removeItem(key: string): void;
}

export interface Options {
  /** The public key from the panel, tk_widget_…. */
  widgetKey: string;
  /** Where Tolkovo runs, e.g. https://support.example.com. */
  apiBase: string;
  /** Default "auto". */
  locale?: Locale | "auto";
  /** The window's title; the widget's name in the panel by default. */
  title?: Localized;
  /** A line under the title, e.g. «Обычно отвечаем за 5 минут». */
  subtitle?: Localized;
  /** The first message the visitor sees, before writing. */
  greeting?: Localized;
  /** The placeholder of the message field. */
  placeholder?: Localized;
  /** An image URL or a data: URI shown in the header. */
  logo?: string;
  theme?: Theme;
  /** Show the round button; false when the app opens the chat itself (Tolkovo.open()). Default true. */
  launcher?: boolean;
  /** Where the widget is added; document.body by default. */
  container?: HTMLElement;
  /** The signed-in user, if known at start; later calls go through identify(). */
  user?: User;
  /** Where the session token is kept. */
  storage?: TokenStorage;
  /** Open the window at start. */
  open?: boolean;
  /** Ask who the visitor is before the first message. No form by default. */
  prechat?: Prechat;
}

/** A file of a message. The link is signed and short-lived; the widget renews it while the chat is open. */
export interface ChatFile {
  name: string;
  contentType: string;
  size: number;
  url: string;
  expiresAt: string;
}

/** A message of the chat as the widget shows it. */
export interface ChatMessage {
  id: string;
  /** contact — the visitor; operator or bot — a reply. */
  sender: "contact" | "operator" | "bot";
  /** Empty for a message of files only. */
  body: string;
  createdAt: string;
  files: ChatFile[];
}

export interface Events {
  /** The session started and the chat is loaded. */
  ready: void;
  open: void;
  close: void;
  /** A new reply arrived. */
  message: ChatMessage;
  /** Unread replies changed. */
  unread: number;
  /** The visitor sent their first message in this session. */
  chatStarted: void;
  /** The visitor left their contact. No names or emails here: only where it came from. */
  contactCollected: { source: "prechat" };
}

export type EventName = keyof Events;
export type Handler<E extends EventName> = (payload: Events[E]) => void;

/** The widget's JavaScript API (docs/04 §6.6). */
export interface TolkovoApi {
  /** Adds the widget to the page. A second call replaces the first widget. */
  init(options: Options): void;
  /** The visitor signed in: continue with their verified contact. */
  identify(user: User): Promise<void>;
  /** The visitor signed out: end the session; the next person starts afresh. */
  logout(): Promise<void>;
  open(): void;
  close(): void;
  toggle(): void;
  /** Show or hide the launcher button. */
  show(): void;
  hide(): void;
  on<E extends EventName>(event: E, handler: Handler<E>): void;
  off<E extends EventName>(event: E, handler: Handler<E>): void;
  /** Removes the widget from the page. */
  destroy(): void;
}
