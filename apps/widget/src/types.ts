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
}

/** A message of the chat as the widget shows it. */
export interface ChatMessage {
  id: string;
  /** contact — the visitor; operator or bot — a reply. */
  sender: "contact" | "operator" | "bot";
  body: string;
  createdAt: string;
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
