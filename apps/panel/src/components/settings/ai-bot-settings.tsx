import type { AIBot, Channel, Topic } from "@tolkovo/api-client";
import { useAIBot, useChannels, useSetAIBot, useTopics } from "@tolkovo/api-client/react";
import { Button, Skeleton } from "@tolkovo/ui";
import { Link } from "@tanstack/react-router";
import { useId, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";

import { describeApiError } from "../../lib/api-errors";
import { FormMessage, TextAreaField, TextField } from "../form/fields";

/** Limits of AIBot in api/openapi.yaml. */
const MAX_HANDOFF = 500;

/**
 * «ИИ-бот отвечает клиентам» (ADR-0018): the channels whose new conversations the bot takes, the
 * topics it leaves to people, how sure it must be and what the customer reads on a handoff. What
 * it knows lives in «База знаний».
 */
export function AIBotSettings({ aiEnabled }: { aiEnabled: boolean }) {
  const { t } = useTranslation();
  const bot = useAIBot();
  const channels = useChannels();
  const topics = useTopics();
  const id = useId();
  const failed = bot.error ?? channels.error ?? topics.error;
  return (
    <section aria-labelledby={id} className="flex flex-col gap-3 rounded-xl border border-line bg-surface p-4">
      <div className="flex flex-col gap-1">
        <h3 id={id} className="m-0 text-[14px] font-semibold">
          {t("settings.ai.bot.title")}
        </h3>
        <p className="m-0 text-[12.5px] text-ink-2">
          {t("settings.ai.bot.lead")}{" "}
          <Link to="/settings/knowledge" className="text-teal underline-offset-2 hover:underline">
            {t("settings.ai.bot.knowledgeLink")}
          </Link>
        </p>
      </div>
      {!aiEnabled ? <p className="m-0 rounded-lg border border-line bg-lamp-soft px-3 py-2 text-[12.5px] text-ink">{t("settings.ai.bot.aiOff")}</p> : null}
      {failed ? (
        <div role="alert" className="flex flex-col items-start gap-2">
          <p className="m-0 text-ink-2">{describeApiError(t, failed).message}</p>
          <Button variant="secondary" onClick={() => void Promise.all([bot.refetch(), channels.refetch(), topics.refetch()])}>
            {t("error.retry")}
          </Button>
        </div>
      ) : bot.data && channels.data && topics.data ? (
        <BotForm bot={bot.data} channels={channels.data.items} topics={topics.data} />
      ) : (
        <div role="status">
          <span className="sr-only">{t("app.loading")}</span>
          <Skeleton className="h-40" />
        </div>
      )}
    </section>
  );
}

function BotForm({ bot, channels, topics }: { bot: AIBot; channels: Channel[]; topics: Topic[] }) {
  const { t } = useTranslation();
  const save = useSetAIBot();
  const uid = useId();
  const [on, setOn] = useState(() => new Set(bot.channel_ids));
  const [off, setOff] = useState(() => new Set(bot.topics_off));
  const [confidence, setConfidence] = useState(String(bot.min_confidence));
  const [handoff, setHandoff] = useState(bot.handoff_message);
  const [error, setError] = useState<string | null>(null);
  const [message, setMessage] = useState<{ tone: "success" | "error"; text: string } | null>(null);
  const toggle = (set: Set<string>, value: string, update: (next: Set<string>) => void) => {
    const next = new Set(set);
    if (next.has(value)) next.delete(value);
    else next.add(value);
    update(next);
    setMessage(null);
  };
  const submit = (e: FormEvent) => {
    e.preventDefault();
    setMessage(null);
    const value = Number(confidence.trim());
    if (!/^\d{1,3}$/.test(confidence.trim()) || value > 100) {
      setError(t("settings.ai.bot.confidenceInvalid"));
      document.getElementById(`${uid}-confidence`)?.focus();
      return;
    }
    setError(null);
    save.mutate(
      { channel_ids: [...on], topics_off: [...off], min_confidence: value, handoff_message: handoff.trim() },
      {
        onSuccess: () => setMessage({ tone: "success", text: t("settings.ai.bot.saved") }),
        onError: (err) => setMessage({ tone: "error", text: describeApiError(t, err).message }),
      },
    );
  };
  return (
    <form noValidate onSubmit={submit} aria-busy={save.isPending} className="flex flex-col gap-4">
      <fieldset disabled={save.isPending} className="m-0 flex flex-col gap-4 border-0 p-0">
        <fieldset className="m-0 flex flex-col gap-2 border-0 p-0">
          <legend className="mb-1 p-0 text-[13px] font-medium text-ink">{t("settings.ai.bot.channels")}</legend>
          {channels.length === 0 ? <p className="m-0 text-[12.5px] text-ink-3">{t("settings.ai.bot.noChannels")}</p> : null}
          {channels.map((c) => (
            <label key={c.id} className="flex items-center gap-2.5 text-[13px] text-ink">
              <input type="checkbox" checked={on.has(c.id)} onChange={() => toggle(on, c.id, setOn)} className="size-4 shrink-0 accent-teal" />
              <span className="min-w-0 truncate">{c.name}</span>
            </label>
          ))}
          <p className="m-0 text-[12.5px] text-ink-3">{t("settings.ai.bot.channelsHint")}</p>
        </fieldset>
        {topics.length > 0 ? (
          <fieldset className="m-0 flex flex-col gap-2 border-0 p-0">
            <legend className="mb-1 p-0 text-[13px] font-medium text-ink">{t("settings.ai.bot.topicsOff")}</legend>
            {topics.map((topic) => (
              <label key={topic.id} className="flex items-center gap-2.5 text-[13px] text-ink">
                <input
                  type="checkbox"
                  checked={off.has(topic.id)}
                  onChange={() => toggle(off, topic.id, setOff)}
                  className="size-4 shrink-0 accent-teal"
                />
                <span className="min-w-0 truncate">{topic.name}</span>
              </label>
            ))}
            <p className="m-0 text-[12.5px] text-ink-3">{t("settings.ai.bot.topicsOffHint")}</p>
          </fieldset>
        ) : null}
        <TextField
          id={`${uid}-confidence`}
          name="min_confidence"
          label={t("settings.ai.bot.confidence")}
          hint={t("settings.ai.bot.confidenceHint")}
          error={error ?? undefined}
          value={confidence}
          onChange={(e) => setConfidence(e.target.value)}
          inputMode="numeric"
          autoComplete="off"
          className="max-w-[120px]"
        />
        <TextAreaField
          id={`${uid}-handoff`}
          name="handoff_message"
          label={t("settings.ai.bot.handoff")}
          hint={t("settings.ai.bot.handoffHint", { max: MAX_HANDOFF })}
          value={handoff}
          rows={2}
          maxLength={MAX_HANDOFF}
          placeholder={t("settings.ai.bot.handoffPlaceholder")}
          onChange={(e) => setHandoff(e.currentTarget.value)}
        />
      </fieldset>
      <div>
        <Button type="submit" disabled={save.isPending}>
          {t("settings.ai.bot.save")}
        </Button>
      </div>
      <FormMessage tone="success" message={message?.tone === "success" ? message.text : null} />
      <FormMessage tone="error" message={message?.tone === "error" ? message.text : null} />
    </form>
  );
}
