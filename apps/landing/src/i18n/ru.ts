// RU copy, taken from the approved mockup design/landing.html and docs/05-landing.md.
import { COMMUNITY_CHAT_URL, REPO_URL } from "../site";
import type { Dict } from "./types";

export const ru: Dict = {
  meta: {
    lang: "ru",
    ogLocale: "ru_RU",
    title: "Tolkovo — open-source аналог Jivo с ИИ-агентом и Telegram",
    description:
      "Сайт, Telegram-бот и Telegram Business в общем инбоксе команды. ИИ отвечает по базе знаний и передаёт сложные диалоги оператору нужной линии. Open source, в облаке или на своём сервере.",
    ogImageAlt: "Tolkovo: все чаты в одном окне. ИИ отвечает, люди решают.",
  },
  header: {
    skip: "Перейти к содержанию",
    navLabel: "Разделы",
    nav: [
      { href: "#features", label: "Возможности" },
      { href: "#channels", label: "Каналы" },
      { href: "#selfhost", label: "Self-host" },
      { href: "#oss", label: "Open source" },
      { href: "#faq", label: "Вопросы" },
    ],
    langLabel: "Язык",
    github: "GitHub",
    cta: "Попробовать",
    homeLabel: "Tolkovo, на главную",
  },
  hero: {
    eyebrow: "Open-source live chat · сайт и Telegram",
    h1: "Все чаты в одном окне. ИИ отвечает,",
    h1Accent: "люди решают.",
    lede:
      "Виджет на сайте, Telegram-бот и Telegram Business приходят в общий инбокс команды. ИИ закрывает типовые вопросы по вашей базе знаний и передаёт сложные оператору нужной линии, с кратким пересказом диалога. В облаке или на вашем сервере.",
    ctaCloud: "Встать в очередь в облако",
    ctaSelf: "Развернуть у себя",
    ctaGithub: "Код на GitHub →",
    trust: "AGPL-3.0 · Go + React · Docker · RU / EN · Без трекеров на этом сайте",
  },
  board: {
    label: "Демонстрация: как диалоги проходят через ИИ к операторам",
    eyebrow: "Коммутатор смены",
    live: "демо · 14:32 МСК",
    colIn: "Откуда пишут",
    colAi: "Первая линия",
    colLines: "Линии и операторы на смене",
    jacks: [
      { id: "web", name: "Сайт", sub: "виджет" },
      { id: "tg", name: "Telegram-бот", sub: "@shop_support_bot" },
      { id: "biz", name: "Telegram Business", sub: "@shop_manager" },
    ],
    aiTitle: "ИИ-агент",
    aiText: "Отвечает по базе знаний. Не уверен, клиент просит человека или тема из списка — зовёт оператора.",
    kAiLabel: "закрыл сам",
    kHLabel: "передал людям",
    lines: [
      {
        id: "L1",
        name: "Поддержка",
        agents: [
          { name: "Анна", load: 2, cap: 4 },
          { name: "Пётр", load: 3, cap: 4 },
        ],
      },
      { id: "L2", name: "Оплата и документы", agents: [{ name: "Олег", load: 1, cap: 3 }] },
      { id: "L3", name: "Технические", agents: [{ name: "Ника", load: 0, cap: 2 }] },
    ],
    ticker: [
      { time: "14:29", kind: "a", text: "Гость #4817 · сайт → ИИ ответил по статье «Возврат товара»" },
      { time: "14:30", kind: "h", text: "Алина Р. · Telegram → L1 · Анна · клиент попросил оператора" },
      { time: "14:31", kind: "a", text: "Гость #4822 · сайт → ИИ ответил по статье «Способы оплаты»" },
    ],
    events: [
      { from: "web", who: "Гость #4821", to: "ai", note: "ИИ ответил по статье «Сроки доставки»" },
      { from: "tg", who: "Иван Петров", to: "L1", agent: "Анна", note: "клиент попросил оператора" },
      { from: "biz", who: "ООО «Ромашка»", to: "L2", agent: "Олег", note: "тема «закрывающие документы»" },
      { from: "web", who: "Гость #4830", to: "ai", note: "ИИ ответил по статье «Смена email»" },
      { from: "tg", who: "Мария К.", to: "L1", agent: "Пётр", note: "готова купить, нужна скидка" },
      { from: "tg", who: "Сергей В.", to: "L3", agent: "Ника", note: "ошибка API, ИИ не нашёл ответа" },
    ],
    channelShort: { web: "сайт", tg: "Telegram", biz: "TG Business" },
    note: "Пример с выдуманными клиентами и цифрами.",
  },
  why: [
    {
      mark: "Telegram без посредников",
      title: "Клиенты уже пишут в Telegram",
      text: [
        "Не заставляйте их искать виджет. Бот компании и Telegram Business-аккаунт приходят в то же окно, что и чат на сайте. Клиент пишет вам лично, отвечает команда.",
      ],
    },
    {
      mark: "ИИ знает свои границы",
      title: "Рутину забирает ИИ",
      text: [
        "Агент отвечает по вашей базе знаний круглосуточно. Если не уверен, клиент раздражён или просит человека, он передаёт диалог оператору и не держит клиента в петле.",
      ],
    },
    {
      mark: "Ваш сервер, ваши ключи",
      title: "Данные остаются у вас",
      text: [
        "Self-host одной командой ",
        { code: "docker compose up" },
        ". Подключайте Claude, OpenAI или локальную модель через Ollama, и переписка не уходит за пределы сервера.",
      ],
    },
  ],
  features: {
    eyebrow: "Возможности",
    title: "Каждому в команде — свой инструмент",
    rows: [
      {
        title: "Оператору",
        who: "весь день в инбоксе",
        feats: [
          { title: "Единый инбокс", text: ["Сайт и Telegram в одной ленте: назначение, заметки для коллег, теги, шаблоны по «/»."] },
          { title: "Подсказки ИИ", text: ["Черновик ответа и пересказ длинного диалога. Отправляете только вы."] },
          { title: "Работа с клавиатуры", text: ["Взять, передать, закрыть, перейти к следующему чату без мыши."] },
          { title: "Карточка клиента", text: ["Прошлые обращения, страница сайта, местное время клиента и его язык."] },
        ],
      },
      {
        title: "Руководителю",
        who: "отвечает за смену и SLA",
        feats: [
          { title: "Линии L1 / L2 / L3", text: ["Эскалация в один клик с причиной. Вторая линия видит историю и заметки первой."] },
          { title: "Смены и распределение", text: ["По очереди или наименее загруженному, лимит чатов на оператора, навыки и языки."] },
          {
            title: "Мониторинг в реальном времени",
            text: ["Кто ждёт дольше всех, кто перегружен, где горит SLA. Переназначить, подсказать, перехватить."],
          },
          { title: "Роли и доступы", text: ["Оператор второй линии не видит чужие очереди, аналитик не пишет клиентам."] },
        ],
      },
      {
        title: "Разработчику",
        who: "разворачивает и встраивает",
        feats: [
          {
            title: "Go + React, один бинарь",
            text: ["Postgres, Redis, S3. Обновление через ", { code: "docker compose pull" }, "."],
          },
          { title: "Любая модель, свои ключи", text: ["Claude, OpenAI, Ollama, vLLM. Без наценки и посредников."] },
          { title: "Виджет под MIT", text: ["Около 35 КБ, Shadow DOM, JS API и проверка пользователя по HMAC."] },
          {
            title: "API и вебхуки",
            text: ["OpenAPI 3.1, события диалогов, свои каналы через Channel API."],
            badge: "к v1.0",
          },
        ],
      },
    ],
  },
  handoff: {
    eyebrow: "Передача человеку",
    title: "ИИ отвечает. Человек подключается вовремя",
    lede: "Клиент всегда может дойти до человека, и ему не нужно повторять вопрос.",
    chatLabel: "Пример диалога",
    chatHead: "Иван Петров · Telegram-бот",
    chatTime: "14:33",
    messages: [
      { kind: "client", who: "Иван", text: "Не приходит код подтверждения при входе" },
      {
        kind: "ai",
        who: "ИИ-агент",
        text: "Проверьте папку «Спам» и что номер указан как +7… Код действует 5 минут, новый можно запросить через 60 секунд.",
      },
      { kind: "client", who: "Иван", text: "Уже проверял. Позовите человека, пожалуйста" },
      { kind: "ai", who: "ИИ-агент", text: "Передаю специалисту. Обычно отвечаем за 2–3 минуты." },
      { kind: "system", text: "передано на L1 · причина: клиент попросил оператора" },
      {
        kind: "operator",
        who: "Анна · L1",
        text: "Иван, вижу: SMS не доходят на ваш номер из-за оператора связи. Отправила код в этот чат, он придёт через минуту.",
      },
    ],
    steps: [
      {
        title: "ИИ берёт диалог первым",
        text: "Отвечает на типовые вопросы сразу и только по вашим материалам. Ответ без источника не отправляется.",
      },
      {
        title: "Срабатывает правило передачи",
        text: "Неуверенность модели, просьба позвать человека, раздражение, темы вроде «оплата» и «возврат». Правила настраиваете вы.",
      },
      {
        title: "Оператор получает контекст",
        text: "Свободный оператор нужной линии видит пересказ, всю историю и черновик ответа.",
      },
    ],
    summaryEyebrow: "Что видит Анна при передаче",
    summaryText:
      "Клиент не получает SMS-код при входе. ИИ предложил проверить «Спам» и формат номера, не помогло. Клиент спокоен, но попросил человека.",
    summaryFacts: [
      { term: "Канал", value: "Telegram-бот, клиент с марта" },
      { term: "Прошлые", value: "2 обращения, оценка 5" },
      { term: "Ждёт", value: "00:41", mono: true },
    ],
  },
  channels: {
    eyebrow: "Каналы",
    title: "Отвечайте там, где пишут клиенты",
    items: [
      { name: "Виджет для сайта", status: "on", label: "в MVP" },
      { name: "Telegram-бот", status: "on", label: "в MVP" },
      { name: "Telegram Business", status: "on", label: "в MVP" },
      { name: "API и вебхуки", status: "soon", label: "к v1.0" },
      { name: "Email", status: "plan", label: "после v1.0" },
      { name: "WhatsApp", status: "plan", label: "после v1.0" },
      { name: "VK / MAX", status: "plan", label: "после v1.0" },
    ],
    footBefore: "Не хватает канала? ",
    footLink: "Проголосуйте в roadmap",
    footAfter: " или напишите коннектор.",
  },
  compare: {
    eyebrow: "Сравнение",
    title: "Чем Tolkovo отличается",
    head: ["Tolkovo", "Jivo", "Chatwoot", "Intercom"],
    rows: [
      {
        label: "Открытый код",
        cells: [
          { text: "да", tone: "yes" },
          { text: "нет", tone: "no" },
          { text: "да", tone: "yes" },
          { text: "нет", tone: "no" },
        ],
      },
      {
        label: "Свой сервер",
        cells: [
          { text: "docker compose", tone: "yes" },
          { text: "нет", tone: "no" },
          { text: "да", tone: "yes" },
          { text: "нет", tone: "no" },
        ],
      },
      {
        label: "Telegram Business",
        cells: [{ text: "да", tone: "yes" }, { text: "уточняется" }, { text: "уточняется" }, { text: "нет", tone: "no" }],
      },
      {
        label: "Выбор модели и свои ключи",
        cells: [
          { text: "включая локальные", tone: "yes" },
          { text: "нет", tone: "no" },
          { text: "частично" },
          { text: "нет", tone: "no" },
        ],
      },
      {
        label: "Оплата",
        cells: [{ text: "бесплатно" }, { text: "за оператора" }, { text: "OSS + платное облако" }, { text: "подписка + плата за ИИ" }],
      },
      {
        label: "Стек",
        cells: [{ text: "Go + React" }, { text: "закрытый" }, { text: "Rails + Vue" }, { text: "закрытый" }],
      },
    ],
    note: "Сравнение по публичной информации. Ячейки «уточняется» проверяются перед публикацией. Нашли неточность — откройте issue.",
  },
  selfhost: {
    eyebrow: "Self-host",
    title: "Свой сервер за пять минут",
    text: "Один compose-файл: сервер на Go, веб-панель, PostgreSQL, Redis. Для небольшой команды хватит VPS на 2 vCPU и 4 ГБ памяти.",
    checks: [
      "Телеметрия выключена, пока вы её не включите",
      "Локальные модели через Ollama или vLLM",
      "Данные только в вашей базе. Подходит под 152-ФЗ при размещении в РФ",
    ],
    termLabel: "Команды установки",
    commentQuick: "# скачать и запустить",
    commentManual: "# или вручную",
    commentOpen: "# откройте https://ваш-домен — мастер создаст владельца",
    copy: "Скопировать",
    copied: "Скопировано",
    selected: "Выделено, нажмите ⌘C",
  },
  oss: {
    eyebrow: "Open source",
    title: "Создаётся открыто",
    text: "Код, roadmap и обсуждения публичны. Предлагайте функции, голосуйте за каналы, присылайте PR. Для первого вклада есть задачи с меткой good first issue.",
    issues: "Good first issues",
    chat: "Чат сообщества в Telegram →",
    licenses: [
      {
        title: "Ядро — AGPL-3.0",
        text: "Пользуйтесь, меняйте и разворачивайте бесплатно. Если запускаете изменённую версию как сервис для других, публикуйте изменения.",
      },
      { title: "Виджет и SDK — MIT", text: "Встраивайте в любой сайт и приложение без условий." },
    ],
  },
  faq: {
    eyebrow: "Вопросы",
    title: "Частые вопросы",
    items: [
      {
        q: "Это правда бесплатно?",
        a: "Да. Код открыт, self-host бесплатен и без ограничений на число операторов. Облако на старте тоже бесплатное. Если появятся платные тарифы, self-host останется бесплатным.",
      },
      {
        q: "Где хранятся данные в облаке?",
        a: "На серверах в Европе. Если компании нужно хранить персональные данные в России по 152-ФЗ, разверните Tolkovo на своём сервере в РФ.",
      },
      {
        q: "Какие модели поддерживаются?",
        a: "Claude, OpenAI и любые OpenAI-совместимые API, включая локальные Ollama и vLLM. Ключи ваши, наценки нет.",
      },
      { q: "Можно работать без ИИ?", a: "Да. ИИ выключается полностью, и Tolkovo работает как обычный live chat." },
      {
        q: "Что такое Telegram Business?",
        a: "Клиенты пишут на ваш личный или бизнес-аккаунт в Telegram, а отвечает команда из инбокса. Подключается через вашего собственного бота, для этого нужен Telegram Premium.",
      },
      { q: "Будут WhatsApp, VK и Email?", a: "Да, после v1.0. Порядок определим голосованием в roadmap." },
    ],
  },
  waitlist: {
    title: "Облако откроется весной 2027",
    text: "Оставьте email, и мы позовём в бету, когда она начнётся. Self-host доступен с первой альфы.",
    emailLabel: "Рабочий email",
    emailPlaceholder: "anna@company.ru",
    channelsLegend: "Какие каналы нужны",
    channels: [
      { value: "telegram", label: "Telegram", checked: true },
      { value: "web", label: "Сайт", checked: true },
      { value: "whatsapp", label: "WhatsApp", checked: false },
      { value: "vk_max", label: "VK / MAX", checked: false },
      { value: "email", label: "Email", checked: false },
    ],
    consent:
      "Даю согласие на обработку моего email и выбранных каналов, чтобы получить приглашение в бету и письма о запуске облака Tolkovo. Согласие можно отозвать в любой момент по ссылке в письме.",
    privacyNote:
      "Пришлём письмо со ссылкой для подтверждения: без него адрес в список не попадёт. Данные хранятся на серверах в ЕС, удалим их по первому запросу.",
    submit: "Встать в очередь",
    sending: "Отправляем…",
    successTitle: "Проверьте почту — подтвердите подписку",
    successText: "Мы отправили письмо на {email}. Нажмите ссылку в нём, и адрес попадёт в очередь. Если письма нет, проверьте «Спам».",
    errEmailEmpty: "Введите email.",
    errEmailFormat: "Введите email в формате name@company.ru.",
    errConsent: "Отметьте согласие, без него мы не можем сохранить email.",
    errUnavailable: "Не получилось отправить: сервер сейчас недоступен. Попробуйте позже или напишите нам в Telegram.",
    errRateLimited: "Слишком много попыток. Подождите минуту и попробуйте снова.",
    errInvalid: "Сервер не принял адрес. Проверьте email и попробуйте снова.",
    errFallbackLink: "Чат сообщества в Telegram",
    honeypotLabel: "Не заполняйте это поле",
  },
  footer: {
    cols: [
      {
        title: "Продукт",
        links: [
          { label: "Возможности", href: "#features" },
          { label: "Каналы", href: "#channels" },
          { label: "Roadmap", href: `${REPO_URL}/issues` },
          { label: "Changelog", href: `${REPO_URL}/releases` },
        ],
      },
      {
        title: "Разработчикам",
        links: [
          { label: "Self-host", href: "#selfhost" },
          { label: "Код на GitHub", href: REPO_URL },
          { label: "CONTRIBUTING", href: `${REPO_URL}/blob/main/CONTRIBUTING.md` },
        ],
      },
      {
        title: "Сообщество",
        links: [
          { label: "Telegram", href: COMMUNITY_CHAT_URL },
          { label: "Discussions", href: `${REPO_URL}/discussions` },
        ],
      },
      {
        title: "Правовое",
        links: [
          { label: "Лицензия", href: `${REPO_URL}/blob/main/LICENSE` },
          { label: "Безопасность", href: `${REPO_URL}/blob/main/SECURITY.md` },
        ],
      },
    ],
    bottom: "© 2026 Tolkovo contributors · Этот сайт не использует cookies",
  },
};
