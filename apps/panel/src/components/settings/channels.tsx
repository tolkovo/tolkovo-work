import type { Channel, TelegramLogin } from "@tolkovo/api-client";
import {
  useCan,
  useChannels,
  useConnectTelegramBot,
  useDisconnectChannel,
  useRenameChannel,
  useStartTelegramUserLogin,
  useSubmitTelegramUserCode,
  useSubmitTelegramUserPassword,
} from "@tolkovo/api-client/react";
import { Button, Skeleton, cn } from "@tolkovo/ui";
import { Bot, Send, UserRound } from "lucide-react";
import { useEffect, useRef, useState, type FormEvent, type ReactNode } from "react";
import type { TFunction } from "i18next";
import { useTranslation } from "react-i18next";

import { describeApiError } from "../../lib/api-errors";
import { formValues } from "../../lib/forms";
import { FormMessage, TextField } from "../form/fields";

/**
 * «Настройки → Каналы» (docs/06 T-01, docs/07 §7, ADR-0014): the workspace's Telegram channels
 * and the connection wizard — a bot by its token, or a personal account by phone, code and cloud
 * password. Secrets never come back from the server; the list shows names, usernames and a
 * masked phone.
 */
export function ChannelsSettings() {
  const { t } = useTranslation();
  const channels = useChannels();
  const canManage = useCan("channels.manage");
  const [wizard, setWizard] = useState(false);
  const [notice, setNotice] = useState<string | null>(null);

  return (
    <section aria-labelledby="settings-channels-title" aria-busy={channels.isPending} className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h2 id="settings-channels-title" className="m-0 text-base font-semibold">
          {t("settings.channels.title")}
        </h2>
        {canManage && channels.data?.encryption_available && !wizard ? (
          <Button
            variant="primary"
            onClick={() => {
              setNotice(null);
              setWizard(true);
            }}
          >
            <Send aria-hidden="true" />
            {t("settings.channels.connect")}
          </Button>
        ) : null}
      </div>
      <p className="m-0 max-w-[640px] text-[13px] text-ink-2">{t("settings.channels.lead")}</p>
      <FormMessage tone="success" message={notice} />

      {channels.isPending ? (
        <div role="status" className="flex max-w-[720px] flex-col gap-2">
          <span className="sr-only">{t("app.loading")}</span>
          <Skeleton className="h-16" />
          <Skeleton className="h-16" />
        </div>
      ) : channels.isError ? (
        <div role="alert" className="flex flex-col items-start gap-2">
          <p className="m-0 text-ink-2">{describeApiError(t, channels.error).message}</p>
          <Button variant="secondary" onClick={() => void channels.refetch()}>
            {t("error.retry")}
          </Button>
        </div>
      ) : (
        <>
          {!channels.data.encryption_available ? (
            <p role="note" className="m-0 max-w-[640px] rounded-lg border border-note-line bg-note px-3 py-2 text-[13px] text-ink">
              {t("settings.channels.noKey")}
            </p>
          ) : null}
          {wizard ? (
            <ConnectTelegram
              userAvailable={channels.data.telegram_user_available}
              onClose={(done) => {
                setWizard(false);
                if (done) setNotice(t(`settings.channels.wizard.done.${done.type}`, { name: done.name }));
              }}
            />
          ) : null}
          <ChannelList channels={channels.data.items} canManage={canManage} onNotice={setNotice} />
        </>
      )}
    </section>
  );
}

function ChannelList({
  channels,
  canManage,
  onNotice,
}: {
  channels: Channel[];
  canManage: boolean;
  onNotice: (message: string | null) => void;
}) {
  const { t } = useTranslation();
  if (channels.length === 0) {
    return <p className="m-0 text-[13px] text-ink-3">{t("settings.channels.empty")}</p>;
  }
  return (
    <ul
      aria-label={t("settings.channels.listLabel")}
      className="m-0 flex max-w-[720px] list-none flex-col divide-y divide-line rounded-xl border border-line bg-surface p-0"
    >
      {channels.map((ch) => (
        <ChannelRow key={ch.id} channel={ch} canManage={canManage} onNotice={onNotice} />
      ))}
    </ul>
  );
}

function ChannelIcon({ type }: { type: Channel["type"] }) {
  const Icon = type === "telegram_bot" ? Bot : UserRound;
  return (
    <span aria-hidden="true" className="grid size-9 shrink-0 place-items-center rounded-lg bg-teal-soft text-teal">
      <Icon className="size-[18px]" strokeWidth={1.75} />
    </span>
  );
}

function StatusBadge({ channel }: { channel: Channel }) {
  const { t } = useTranslation();
  return (
    <span
      className={cn(
        "inline-flex items-center rounded-full border px-2 py-px text-[12px] whitespace-nowrap",
        // Text stays --ink on the soft fills: the -ink tokens are for solid backgrounds.
        channel.status === "active" && "border-teal bg-teal-soft text-ink",
        channel.status === "pending" && "border-line bg-sunk text-ink-2",
        channel.status === "error" && "border-danger bg-danger-soft text-ink",
      )}
    >
      {t(`settings.channels.status.${channel.status}`)}
    </span>
  );
}

/** «@username · +79•••67 · сервер сам забирает сообщения»: what tells channels apart. */
function channelFacts(t: TFunction, ch: Channel): string {
  const d = ch.details;
  return [
    t(`settings.channels.type.${ch.type}`),
    d.username ? `@${d.username}` : null,
    d.phone_masked,
    d.delivery ? t(`settings.channels.delivery.${d.delivery}`) : null,
  ]
    .filter(Boolean)
    .join(" · ");
}

function ChannelRow({
  channel,
  canManage,
  onNotice,
}: {
  channel: Channel;
  canManage: boolean;
  onNotice: (message: string | null) => void;
}) {
  const { t } = useTranslation();
  const rename = useRenameChannel();
  const disconnect = useDisconnectChannel();
  const [mode, setMode] = useState<"view" | "rename" | "confirm">("view");
  const [error, setError] = useState<string | null>(null);
  const confirmRef = useRef<HTMLButtonElement>(null);
  const pending = channel.status === "pending";

  useEffect(() => {
    if (mode === "confirm") confirmRef.current?.focus();
  }, [mode]);

  const onRename = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const name = (formValues(event.currentTarget).name ?? "").trim();
    if (!name) return;
    setError(null);
    rename.mutate(
      { id: channel.id, name },
      { onSuccess: () => setMode("view"), onError: (e) => setError(describeApiError(t, e).message) },
    );
  };

  const onDisconnect = () => {
    setError(null);
    onNotice(null);
    disconnect.mutate(channel.id, {
      onSuccess: () => {
        if (!pending) onNotice(t("settings.channels.disconnected", { name: channel.name }));
      },
      onError: (e) => {
        setMode("view");
        setError(describeApiError(t, e).message);
      },
    });
  };

  return (
    <li className="flex flex-col gap-2 px-3 py-3">
      <div className="flex items-start gap-3">
        <ChannelIcon type={channel.type} />
        <div className="flex min-w-0 flex-1 flex-col gap-0.5">
          {mode === "rename" ? (
            <form onSubmit={onRename} className="flex flex-wrap items-end gap-2" aria-busy={rename.isPending}>
              <div className="min-w-0 flex-1 basis-56">
                <TextField
                  id={`channel-name-${channel.id}`}
                  name="name"
                  label={t("settings.channels.renameLabel")}
                  defaultValue={channel.name}
                  maxLength={120}
                  required
                  autoFocus
                />
              </div>
              <Button type="submit" variant="primary" disabled={rename.isPending}>
                {t("settings.channels.save")}
              </Button>
              <Button variant="ghost" onClick={() => setMode("view")}>
                {t("settings.channels.cancel")}
              </Button>
            </form>
          ) : (
            <div className="flex flex-wrap items-center gap-2">
              <span className="text-[14px] font-medium break-words text-ink">{channel.name}</span>
              <StatusBadge channel={channel} />
            </div>
          )}
          <span className="text-[12.5px] break-words text-ink-3">{channelFacts(t, channel)}</span>
          {pending ? <span className="text-[12.5px] text-ink-3">{t("settings.channels.pendingHint")}</span> : null}
        </div>
        {canManage && mode === "view" ? (
          <div className="flex shrink-0 gap-1 max-nav:flex-col">
            {!pending ? (
              <Button variant="ghost" size="sm" onClick={() => setMode("rename")}>
                {t("settings.channels.rename")}
              </Button>
            ) : null}
            <Button variant="ghost" size="sm" onClick={() => setMode("confirm")}>
              {pending ? t("settings.channels.cancelLogin") : t("settings.channels.disconnect")}
            </Button>
          </div>
        ) : null}
      </div>
      {mode === "confirm" ? (
        <div
          role="group"
          aria-label={t("settings.channels.disconnect")}
          className="ml-12 flex flex-col gap-2 rounded-lg border border-line bg-sunk p-3 max-nav:ml-0"
        >
          <p className="m-0 text-[13px] text-ink">
            {pending
              ? t("settings.channels.cancelLoginConfirm")
              : t(`settings.channels.disconnectConfirm.${channel.type}`, { name: channel.name })}
          </p>
          <div className="flex flex-wrap gap-2">
            <Button ref={confirmRef} variant="destructive" disabled={disconnect.isPending} onClick={onDisconnect}>
              {pending ? t("settings.channels.cancelLogin") : t("settings.channels.disconnect")}
            </Button>
            <Button variant="secondary" disabled={disconnect.isPending} onClick={() => setMode("view")}>
              {t("settings.channels.keep")}
            </Button>
          </div>
        </div>
      ) : null}
      <FormMessage tone="error" message={error} />
    </li>
  );
}

type Step =
  | { kind: "choose" }
  | { kind: "bot" }
  | { kind: "phone" }
  | { kind: "code"; login: TelegramLogin; phone: string }
  | { kind: "password"; channel: Channel };

/**
 * The wizard (docs/07 §7): what is needed is said before it starts, every step is checked by
 * the server at once. A personal account is warned about first: its chats become visible to
 * the operators, and Telegram may restrict accounts run by software (ADR-0014).
 */
function ConnectTelegram({
  userAvailable,
  onClose,
}: {
  userAvailable: boolean;
  onClose: (done?: Channel) => void;
}) {
  const { t } = useTranslation();
  const [step, setStep] = useState<Step>({ kind: "choose" });
  const cancelLogin = useDisconnectChannel();
  const titleRef = useRef<HTMLHeadingElement>(null);

  // Each step announces itself: the heading takes the focus.
  useEffect(() => {
    titleRef.current?.focus();
  }, [step.kind]);

  const pendingId = step.kind === "code" ? step.login.channel.id : step.kind === "password" ? step.channel.id : null;
  const close = () => {
    // A login left half-way is cancelled, so its phone does not wait 15 minutes on the server.
    if (pendingId) cancelLogin.mutate(pendingId);
    onClose();
  };

  return (
    <section
      aria-labelledby="connect-telegram-title"
      className="flex max-w-[720px] flex-col gap-4 rounded-xl border border-line bg-surface p-4"
    >
      <div className="flex items-center justify-between gap-3">
        <h3 id="connect-telegram-title" ref={titleRef} tabIndex={-1} className="m-0 text-sm font-semibold outline-none">
          {t(`settings.channels.wizard.steps.${step.kind}`)}
        </h3>
        <Button variant="ghost" size="sm" onClick={close}>
          {t("settings.channels.wizard.close")}
        </Button>
      </div>
      {step.kind === "choose" ? (
        <ChooseKind userAvailable={userAvailable} onChoose={(kind) => setStep({ kind })} />
      ) : step.kind === "bot" ? (
        <BotStep onBack={() => setStep({ kind: "choose" })} onDone={onClose} />
      ) : step.kind === "phone" ? (
        <PhoneStep
          onBack={() => setStep({ kind: "choose" })}
          onSent={(login, phone) => setStep({ kind: "code", login, phone })}
        />
      ) : step.kind === "code" ? (
        <CodeStep
          login={step.login}
          phone={step.phone}
          onOtherNumber={() => {
            cancelLogin.mutate(step.login.channel.id);
            setStep({ kind: "phone" });
          }}
          onPassword={(channel) => setStep({ kind: "password", channel })}
          onDone={onClose}
        />
      ) : (
        <PasswordStep channel={step.channel} onDone={onClose} />
      )}
    </section>
  );
}

function ChooseKind({ userAvailable, onChoose }: { userAvailable: boolean; onChoose: (kind: "bot" | "phone") => void }) {
  const { t } = useTranslation();
  const [kind, setKind] = useState<"bot" | "phone">("bot");
  const option = (value: "bot" | "phone", icon: ReactNode, key: "bot" | "user", disabled = false) => (
    <label
      className={cn(
        "flex cursor-pointer items-start gap-3 rounded-lg border p-3",
        kind === value ? "border-teal bg-teal-soft" : "border-line bg-surface hover:bg-hover",
        disabled && "cursor-not-allowed opacity-70 hover:bg-surface",
      )}
    >
      <input
        type="radio"
        name="channel-kind"
        value={value}
        checked={kind === value}
        disabled={disabled}
        onChange={() => setKind(value)}
        aria-describedby={`channel-kind-${key}-text`}
        className="mt-1 size-4 shrink-0 accent-teal"
      />
      <span aria-hidden="true" className="mt-0.5 text-teal">
        {icon}
      </span>
      <span className="flex min-w-0 flex-col gap-0.5">
        <span className="text-[14px] font-medium text-ink">{t(`settings.channels.wizard.${key}.title`)}</span>
        <span id={`channel-kind-${key}-text`} className="text-[12.5px] text-ink-2">
          {t(`settings.channels.wizard.${key}.text`)}{" "}
          {disabled ? t("settings.channels.wizard.user.unavailable") : t(`settings.channels.wizard.${key}.needs`)}
        </span>
      </span>
    </label>
  );
  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        onChoose(kind);
      }}
      className="flex flex-col gap-3"
    >
      <fieldset className="m-0 flex flex-col gap-2 border-0 p-0">
        <legend className="sr-only">{t("settings.channels.wizard.steps.choose")}</legend>
        {option("bot", <Bot className="size-5" strokeWidth={1.75} />, "bot")}
        {option("phone", <UserRound className="size-5" strokeWidth={1.75} />, "user", !userAvailable)}
      </fieldset>
      <div>
        <Button type="submit" variant="primary">
          {t("settings.channels.wizard.next")}
        </Button>
      </div>
    </form>
  );
}

/** Runs a wizard step: one server call, an error that may belong to the step's field. */
function useStepError<F extends string>(fields: Partial<Record<string, F>>) {
  const { t } = useTranslation();
  const [error, setError] = useState<{ field?: F; message: string } | null>(null);
  return {
    error,
    clear: () => setError(null),
    fail: (e: unknown) => setError(describeApiError<F>(t, e, fields)),
    fieldError: (field: F) => (error?.field === field ? error.message : undefined),
    formError: error && !error.field ? error.message : null,
  };
}

function BotStep({ onBack, onDone }: { onBack: () => void; onDone: (channel: Channel) => void }) {
  const { t } = useTranslation();
  const connect = useConnectTelegramBot();
  const step = useStepError<"token">({ telegram_token_invalid: "token", channel_exists: "token" });

  const onSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const values = formValues(event.currentTarget);
    const token = (values.token ?? "").trim();
    const name = (values.name ?? "").trim();
    step.clear();
    connect.mutate({ token, ...(name ? { name } : {}) }, { onSuccess: onDone, onError: step.fail });
  };

  return (
    <form onSubmit={onSubmit} noValidate aria-busy={connect.isPending} className="flex flex-col gap-3">
      <ol className="m-0 flex list-decimal flex-col gap-1 pl-5 text-[13px] text-ink-2">
        <li>{t("settings.channels.wizard.token.step1")}</li>
        <li>{t("settings.channels.wizard.token.step2")}</li>
        <li>{t("settings.channels.wizard.token.step3")}</li>
      </ol>
      <FormMessage tone="error" message={step.formError} />
      <fieldset disabled={connect.isPending} className="m-0 flex flex-col gap-3 border-0 p-0">
        <TextField
          id="bot-token"
          name="token"
          label={t("settings.channels.wizard.token.label")}
          autoComplete="off"
          spellCheck={false}
          required
          className="font-mono text-[12.5px]"
          error={step.fieldError("token")}
        />
        <TextField
          id="bot-name"
          name="name"
          label={t("settings.channels.wizard.token.name")}
          hint={t("settings.channels.wizard.token.nameHint")}
          maxLength={120}
          autoComplete="off"
        />
      </fieldset>
      <div className="flex flex-wrap gap-2">
        <Button type="submit" variant="primary" disabled={connect.isPending}>
          {connect.isPending ? t("settings.channels.wizard.token.pending") : t("settings.channels.wizard.token.submit")}
        </Button>
        <Button variant="ghost" disabled={connect.isPending} onClick={onBack}>
          {t("settings.channels.wizard.back")}
        </Button>
      </div>
    </form>
  );
}

function PhoneStep({ onBack, onSent }: { onBack: () => void; onSent: (login: TelegramLogin, phone: string) => void }) {
  const { t } = useTranslation();
  const start = useStartTelegramUserLogin();
  const [agreed, setAgreed] = useState(false);
  const step = useStepError<"phone">({
    telegram_phone_invalid: "phone",
    telegram_no_account: "phone",
    telegram_banned: "phone",
    invalid_request: "phone",
  });

  const onSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const phone = (formValues(event.currentTarget).phone ?? "").trim();
    step.clear();
    start.mutate(phone, { onSuccess: (login) => onSent(login, phone), onError: step.fail });
  };

  return (
    <form onSubmit={onSubmit} noValidate aria-busy={start.isPending} className="flex flex-col gap-3">
      <div className="flex flex-col gap-2 rounded-lg border border-note-line bg-note p-3">
        <p className="m-0 text-[13px] font-medium text-ink">{t("settings.channels.wizard.risk.title")}</p>
        <ul className="m-0 flex list-disc flex-col gap-1 pl-5 text-[13px] text-ink">
          <li>{t("settings.channels.wizard.risk.work")}</li>
          <li>{t("settings.channels.wizard.risk.limit")}</li>
          <li>{t("settings.channels.wizard.risk.code")}</li>
        </ul>
        <label className="flex items-start gap-2 text-[13px] text-ink">
          <input
            type="checkbox"
            checked={agreed}
            onChange={(e) => setAgreed(e.target.checked)}
            className="mt-0.5 size-4 shrink-0 accent-teal"
          />
          {t("settings.channels.wizard.risk.agree")}
        </label>
      </div>
      <FormMessage tone="error" message={step.formError} />
      <fieldset disabled={start.isPending} className="m-0 flex max-w-[360px] flex-col gap-3 border-0 p-0">
        <TextField
          id="tg-phone"
          name="phone"
          type="tel"
          label={t("settings.channels.wizard.phone.label")}
          hint={t("settings.channels.wizard.phone.hint")}
          autoComplete="off"
          inputMode="tel"
          required
          error={step.fieldError("phone")}
        />
      </fieldset>
      <div className="flex flex-wrap gap-2">
        <Button type="submit" variant="primary" disabled={!agreed || start.isPending}>
          {start.isPending ? t("settings.channels.wizard.phone.pending") : t("settings.channels.wizard.phone.submit")}
        </Button>
        <Button variant="ghost" disabled={start.isPending} onClick={onBack}>
          {t("settings.channels.wizard.back")}
        </Button>
      </div>
    </form>
  );
}

function CodeStep({
  login,
  phone,
  onOtherNumber,
  onPassword,
  onDone,
}: {
  login: TelegramLogin;
  phone: string;
  onOtherNumber: () => void;
  onPassword: (channel: Channel) => void;
  onDone: (channel: Channel) => void;
}) {
  const { t } = useTranslation();
  const submit = useSubmitTelegramUserCode();
  const step = useStepError<"code">({ telegram_code_invalid: "code", invalid_request: "code" });

  const onSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const code = (formValues(event.currentTarget).code ?? "").trim();
    step.clear();
    submit.mutate(
      { id: login.channel.id, code },
      {
        onSuccess: (next) => (next.next === "password" ? onPassword(next.channel) : onDone(next.channel)),
        onError: step.fail,
      },
    );
  };

  return (
    <form onSubmit={onSubmit} noValidate aria-busy={submit.isPending} className="flex flex-col gap-3">
      <p className="m-0 text-[13px] text-ink-2">
        {t(`settings.channels.wizard.code.${login.code_via ?? "other"}`, { phone })}
      </p>
      <FormMessage tone="error" message={step.formError} />
      <fieldset disabled={submit.isPending} className="m-0 flex max-w-[240px] flex-col gap-3 border-0 p-0">
        <TextField
          id="tg-code"
          name="code"
          label={t("settings.channels.wizard.code.label")}
          autoComplete="one-time-code"
          inputMode="numeric"
          maxLength={16}
          required
          className="font-mono tracking-widest"
          error={step.fieldError("code")}
        />
      </fieldset>
      <div className="flex flex-wrap gap-2">
        <Button type="submit" variant="primary" disabled={submit.isPending}>
          {submit.isPending ? t("settings.channels.wizard.code.pending") : t("settings.channels.wizard.code.submit")}
        </Button>
        <Button variant="ghost" disabled={submit.isPending} onClick={onOtherNumber}>
          {t("settings.channels.wizard.code.otherNumber")}
        </Button>
      </div>
    </form>
  );
}

function PasswordStep({ channel, onDone }: { channel: Channel; onDone: (channel: Channel) => void }) {
  const { t } = useTranslation();
  const submit = useSubmitTelegramUserPassword();
  const step = useStepError<"password">({ telegram_password_invalid: "password" });

  const onSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const form = event.currentTarget;
    const password = formValues(form).password ?? "";
    step.clear();
    submit.mutate(
      { id: channel.id, password },
      {
        onSuccess: (next) => onDone(next.channel),
        onError: (e) => {
          form.reset();
          step.fail(e);
        },
      },
    );
  };

  return (
    <form onSubmit={onSubmit} noValidate aria-busy={submit.isPending} className="flex flex-col gap-3">
      <p className="m-0 text-[13px] text-ink-2">{t("settings.channels.wizard.password.text")}</p>
      <FormMessage tone="error" message={step.formError} />
      <fieldset disabled={submit.isPending} className="m-0 flex max-w-[360px] flex-col gap-3 border-0 p-0">
        <TextField
          id="tg-password"
          name="password"
          type="password"
          label={t("settings.channels.wizard.password.label")}
          hint={t("settings.channels.wizard.password.hint")}
          autoComplete="off"
          required
          error={step.fieldError("password")}
        />
      </fieldset>
      <div>
        <Button type="submit" variant="primary" disabled={submit.isPending}>
          {submit.isPending ? t("settings.channels.wizard.password.pending") : t("settings.channels.wizard.password.submit")}
        </Button>
      </div>
    </form>
  );
}

