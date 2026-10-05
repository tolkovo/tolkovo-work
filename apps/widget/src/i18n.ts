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
    attach: "Прикрепить файл",
    remove: "Убрать {name}",
    dismiss: "Убрать",
    drop: "Отпустите, чтобы прикрепить",
    fileTooBig: "«{name}» больше 10 МБ.",
    fileType: "«{name}»: такие файлы не принимаются. Подойдут картинки, PDF, текст, таблицы и архивы.",
    tooManyFiles: "Не больше 5 файлов в сообщении.",
    fileRejected: "Файл «{name}» не прошёл проверку и не отправлен.",
    scanUnavailable: "Файлы сейчас не проверить. Повторите через минуту.",
    image: "Картинка {name}",
    prechatIntro: "Представьтесь, чтобы мы знали, как к вам обращаться и куда ответить.",
    name: "Имя",
    email: "Почта",
    phone: "Телефон",
    consent: "Даю согласие на обработку персональных данных",
    policy: "Политика обработки данных",
    start: "Начать чат",
    required: "Заполните поле.",
    badEmail: "Похоже, в адресе ошибка: нужен вид name@example.com.",
    badPhone: "Цифры, пробелы, скобки и дефис, можно с + в начале.",
    needConsent: "Без согласия начать чат нельзя.",
    prechatFailed: "Не удалось отправить. Повторите.",
    offlineIntro: "Сейчас никого нет на месте. Оставьте сообщение и контакт — ответим, как только вернёмся.",
    message: "Сообщение",
    needReach: "Оставьте почту или телефон, чтобы мы могли ответить.",
    needConsentToSend: "Без согласия сообщение не отправить.",
    leave: "Отправить",
    away: "Сейчас никого нет на месте. Ответим, как только вернёмся.",
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
    attach: "Attach a file",
    remove: "Remove {name}",
    dismiss: "Remove",
    drop: "Drop to attach",
    fileTooBig: "“{name}” is larger than 10 MB.",
    fileType: "“{name}”: this kind of file is not accepted. Pictures, PDF, text, spreadsheets and archives are.",
    tooManyFiles: "At most 5 files per message.",
    fileRejected: "The file “{name}” did not pass the check and was not sent.",
    scanUnavailable: "Files cannot be checked right now. Try again in a minute.",
    image: "Picture {name}",
    prechatIntro: "Tell us who you are, so we know how to address you and where to reply.",
    name: "Name",
    email: "Email",
    phone: "Phone",
    consent: "I agree to the processing of my personal data",
    policy: "Privacy policy",
    start: "Start chat",
    required: "Fill in this field.",
    badEmail: "This address looks wrong: it should look like name@example.com.",
    badPhone: "Digits, spaces, brackets and hyphens, a + at the start is fine.",
    needConsent: "The chat cannot start without your consent.",
    prechatFailed: "Could not send. Try again.",
    offlineIntro: "Nobody is here right now. Leave a message and a way to reach you, and we will reply once we are back.",
    message: "Message",
    needReach: "Leave an email or a phone, so we can reply.",
    needConsentToSend: "The message cannot be sent without your consent.",
    leave: "Send",
    away: "Nobody is here right now. We will reply once we are back.",
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
