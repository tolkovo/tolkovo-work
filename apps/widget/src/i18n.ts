import type { Locale, Localized } from "./types";

/** The widget's own strings: it does not load the panel's dictionaries. */
const dictionaries = {
  ru: {
    open: "Открыть чат",
    close: "Закрыть чат",
    title: "Поддержка",
    placeholder: "Напишите сообщение…",
    send: "Отправить",
    typing: "Оператор печатает…",
    sending: "Отправляется…",
    failed: "Не отправлено.",
    retry: "Повторить",
    unread: "Новых сообщений: {n}",
    log: "Переписка",
    you: "Вы",
    support: "Поддержка",
    connecting: "Подключаемся…",
    offline: "Нет связи с сервером. Сообщения уйдут, когда она появится.",
    unavailable: "Чат сейчас недоступен. Попробуйте позже.",
    tooLong: "Сообщение длиннее 10 000 символов.",
    file: "Файл",
  },
  en: {
    open: "Open chat",
    close: "Close chat",
    title: "Support",
    placeholder: "Write a message…",
    send: "Send",
    typing: "An agent is typing…",
    sending: "Sending…",
    failed: "Not sent.",
    retry: "Retry",
    unread: "New messages: {n}",
    log: "Conversation",
    you: "You",
    support: "Support",
    connecting: "Connecting…",
    offline: "No connection to the server. Messages go out once it is back.",
    unavailable: "The chat is unavailable right now. Try again later.",
    tooLong: "The message is longer than 10,000 characters.",
    file: "File",
  },
} as const;

export type MessageKey = keyof (typeof dictionaries)["ru"];
export type Translate = (key: MessageKey, vars?: Record<string, string | number>) => string;

/** The locale: the option, then <html lang>, then the browser's language. */
export function resolveLocale(option: Locale | "auto" | undefined): Locale {
  if (option === "ru" || option === "en") return option;
  const candidates = [document.documentElement.lang, ...(navigator.languages ?? []), navigator.language];
  for (const c of candidates) {
    const lang = c?.slice(0, 2).toLowerCase();
    if (lang === "ru" || lang === "en") return lang;
  }
  return "en";
}

export function translator(locale: Locale): Translate {
  const dict = dictionaries[locale];
  return (key, vars) => dict[key].replace(/\{(\w+)\}/g, (_, name: string) => String(vars?.[name] ?? ""));
}

/** Picks the text of a Localized for the locale, falling back to the other language. */
export function localized(value: Localized | undefined, locale: Locale): string | undefined {
  if (value === undefined) return undefined;
  if (typeof value === "string") return value;
  return value[locale] ?? value.ru ?? value.en;
}
