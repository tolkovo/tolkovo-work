import type { KBArticle, KBArticleKind, KBImport, Topic } from "@tolkovo/api-client";
import { useKBArticle, useKBArticles, useKBGuide, useKBMutations, useKBSearch, useSetKBGuide, useTopics } from "@tolkovo/api-client/react";
import { Button, Skeleton, cn } from "@tolkovo/ui";
import { useId, useState, type ChangeEvent, type FormEvent } from "react";
import { useTranslation } from "react-i18next";

import { describeApiError } from "../../lib/api-errors";
import { FormMessage, SelectField, TextAreaField, TextField } from "../form/fields";

/** Limits of the knowledge base (KBArticleInput and KBGuide in api/openapi.yaml). */
const MAX_TITLE = 200;
const MAX_BODY = 20000;
const MAX_URL = 500;
const MAX_GUIDE = 3000;
const MAX_ARTICLES = 1000;
/** A knowledge file is at most 16 MiB (POST /kb/import). */
const MAX_FILE_BYTES = 16 << 20;
const FORMAT = "tolkovo-knowledge/1";

/**
 * «База знаний» (ADR-0018): what the AI bot answers from. The guide goes into every prompt; the
 * search picks a few entries for each customer message, so the model never reads the whole base.
 * An AI agent of the app's developers can write the whole base as one file
 * (guides/knowledge-base-brief.md), imported here.
 */
export function KnowledgeSettingsPage() {
  const { t } = useTranslation();
  return (
    <section aria-labelledby="settings-knowledge-title" className="flex max-w-[760px] flex-col gap-4">
      <div className="flex flex-col gap-1">
        <h2 id="settings-knowledge-title" className="m-0 text-base font-semibold">
          {t("settings.knowledge.title")}
        </h2>
        <p className="m-0 text-[13px] text-ink-2">{t("settings.knowledge.lead")}</p>
      </div>
      <GuideForm />
      <ImportFile />
      <SearchCheck />
      <Articles />
    </section>
  );
}

function Panel({ title, lead, children }: { title: string; lead?: string; children: React.ReactNode }) {
  const id = useId();
  return (
    <section aria-labelledby={id} className="flex flex-col gap-3 rounded-xl border border-line bg-surface p-4">
      <div className="flex flex-col gap-1">
        <h3 id={id} className="m-0 text-[14px] font-semibold">
          {title}
        </h3>
        {lead ? <p className="m-0 text-[12.5px] text-ink-2">{lead}</p> : null}
      </div>
      {children}
    </section>
  );
}

function GuideForm() {
  const { t } = useTranslation();
  const guide = useKBGuide();
  // Here, not in the fields: saving changes updated_at, which remounts them.
  const [saved, setSaved] = useState(false);
  if (guide.isPending) {
    return (
      <div role="status">
        <span className="sr-only">{t("app.loading")}</span>
        <Skeleton className="h-48" />
      </div>
    );
  }
  if (guide.isError) {
    return (
      <div role="alert" className="flex flex-col items-start gap-2">
        <p className="m-0 text-ink-2">{describeApiError(t, guide.error).message}</p>
        <Button onClick={() => void guide.refetch()}>{t("error.retry")}</Button>
      </div>
    );
  }
  // Keyed by the saved time: an import that changed the guide refills the form.
  return (
    <GuideFields
      key={guide.data.updated_at ?? "new"}
      about={guide.data.about}
      rules={guide.data.handoff_rules}
      checklist={guide.data.task_checklist}
      saved={saved}
      setSaved={setSaved}
    />
  );
}

function GuideFields(props: { about: string; rules: string; checklist: string; saved: boolean; setSaved: (v: boolean) => void }) {
  const { saved, setSaved } = props;
  const { t } = useTranslation();
  const save = useSetKBGuide();
  const [about, setAbout] = useState(props.about);
  const [rules, setRules] = useState(props.rules);
  const [checklist, setChecklist] = useState(props.checklist);
  const submit = (e: FormEvent) => {
    e.preventDefault();
    setSaved(false);
    save.mutate({ about, handoff_rules: rules, task_checklist: checklist }, { onSuccess: () => setSaved(true) });
  };
  return (
    <Panel title={t("settings.knowledge.guideTitle")} lead={t("settings.knowledge.guideLead")}>
      <form noValidate onSubmit={submit} className="flex flex-col gap-3">
        <TextAreaField
          id="kb-about"
          name="about"
          label={t("settings.knowledge.about")}
          hint={t("settings.knowledge.aboutHint", { max: MAX_GUIDE })}
          value={about}
          rows={6}
          maxLength={MAX_GUIDE}
          onChange={(e) => {
            setAbout(e.currentTarget.value);
            setSaved(false);
          }}
        />
        <TextAreaField
          id="kb-rules"
          name="handoff_rules"
          label={t("settings.knowledge.rules")}
          hint={t("settings.knowledge.rulesHint", { max: MAX_GUIDE })}
          value={rules}
          rows={5}
          maxLength={MAX_GUIDE}
          onChange={(e) => setRules(e.currentTarget.value)}
        />
        <TextAreaField
          id="kb-checklist"
          name="task_checklist"
          label={t("settings.knowledge.checklist")}
          hint={t("settings.knowledge.checklistHint", { max: MAX_GUIDE })}
          value={checklist}
          rows={6}
          maxLength={MAX_GUIDE}
          placeholder={t("settings.knowledge.checklistPlaceholder")}
          onChange={(e) => setChecklist(e.currentTarget.value)}
        />
        <div>
          <Button type="submit" size="sm" variant="primary" disabled={save.isPending}>
            {t("settings.knowledge.guideSave")}
          </Button>
        </div>
        <FormMessage tone="success" message={saved ? t("settings.knowledge.guideSaved") : null} />
        <FormMessage tone="error" message={save.isError ? describeApiError(t, save.error).message : null} />
      </form>
    </Panel>
  );
}

function ImportFile() {
  const { t } = useTranslation();
  const { importFile } = useKBMutations();
  const [problem, setProblem] = useState<string | null>(null);
  const [done, setDone] = useState<string | null>(null);
  const id = useId();

  const pick = async (e: ChangeEvent<HTMLInputElement>) => {
    const input = e.currentTarget;
    const file = input.files?.[0];
    input.value = "";
    setProblem(null);
    setDone(null);
    if (!file) return;
    if (file.size > MAX_FILE_BYTES) {
      setProblem(t("settings.knowledge.importTooLarge"));
      return;
    }
    let body: KBImport;
    try {
      body = JSON.parse(await file.text()) as KBImport;
    } catch {
      setProblem(t("settings.knowledge.importNotJson"));
      return;
    }
    if (typeof body !== "object" || body === null || body.format !== FORMAT || !Array.isArray(body.articles)) {
      setProblem(t("settings.knowledge.importWrongFormat", { expected: FORMAT }));
      return;
    }
    importFile.mutate(body, {
      onSuccess: (r) => {
        const text = t("settings.knowledge.imported", { created: r.created, updated: r.updated });
        setDone(
          r.unknown_topics.length > 0
            ? `${text} ${t("settings.knowledge.unknownTopics", { names: r.unknown_topics.join(", ") })}`
            : text,
        );
      },
      onError: (err) => setProblem(describeApiError(t, err).message),
    });
  };

  return (
    <Panel title={t("settings.knowledge.importTitle")} lead={t("settings.knowledge.importLead")}>
      <div className="flex flex-wrap items-center gap-2">
        <label
          htmlFor={id}
          className={cn(
            "inline-flex h-8 cursor-pointer items-center rounded-lg border border-line px-3 text-[13px] hover:bg-hover",
            "has-[:focus-visible]:outline-2 has-[:focus-visible]:outline-teal",
            importFile.isPending && "pointer-events-none opacity-55",
          )}
        >
          {importFile.isPending ? t("settings.knowledge.importing") : t("settings.knowledge.importPick")}
          <input id={id} type="file" accept=".json,application/json" className="sr-only" onChange={(e) => void pick(e)} />
        </label>
      </div>
      <FormMessage tone="success" message={done} />
      <FormMessage tone="error" message={problem} />
    </Panel>
  );
}

function SearchCheck() {
  const { t } = useTranslation();
  const topics = useTopics();
  const [draft, setDraft] = useState("");
  const [topic, setTopic] = useState("");
  const [asked, setAsked] = useState<{ q: string; topic: string | null }>({ q: "", topic: null });
  const search = useKBSearch(asked.q, asked.topic);
  const submit = (e: FormEvent) => {
    e.preventDefault();
    setAsked({ q: draft.trim(), topic: topic || null });
  };
  return (
    <Panel title={t("settings.knowledge.searchTitle")} lead={t("settings.knowledge.searchLead")}>
      <form noValidate onSubmit={submit} role="search" className="flex flex-col gap-3">
        <TextField
          id="kb-search"
          name="q"
          label={t("settings.knowledge.searchQuestion")}
          value={draft}
          maxLength={1000}
          placeholder={t("settings.knowledge.searchPlaceholder")}
          onChange={(e) => setDraft(e.currentTarget.value)}
        />
        {(topics.data?.length ?? 0) > 0 && (
          <SelectField id="kb-search-topic" name="topic" label={t("settings.knowledge.searchTopic")} value={topic} onChange={(e) => setTopic(e.currentTarget.value)}>
            <option value="">{t("settings.knowledge.anyTopic")}</option>
            {topics.data?.map((x) => (
              <option key={x.id} value={x.id}>
                {x.name}
              </option>
            ))}
          </SelectField>
        )}
        <div>
          <Button type="submit" size="sm" disabled={draft.trim() === ""}>
            {t("settings.knowledge.searchSubmit")}
          </Button>
        </div>
      </form>
      {asked.q !== "" && (
        <div aria-live="polite" className="flex flex-col gap-2">
          {search.isPending ? (
            <p className="m-0 text-[13px] text-ink-3">{t("app.loading")}</p>
          ) : search.isError ? (
            <p role="alert" className="m-0 text-[13px] text-danger">
              {describeApiError(t, search.error).message}
            </p>
          ) : search.data.length === 0 ? (
            <p className="m-0 text-[13px] text-ink-2">{t("settings.knowledge.searchNothing")}</p>
          ) : (
            <>
              <p className="m-0 text-[12.5px] text-ink-2">{t("settings.knowledge.searchFound", { count: search.data.length })}</p>
              <ol className="m-0 flex flex-col gap-2 pl-5">
                {search.data.map((h) => (
                  <li key={h.id} className="text-[13px]">
                    <span className="font-medium">{h.title}</span> <KindBadge kind={h.kind} />
                    <p className="m-0 line-clamp-2 text-[12.5px] text-ink-2">{h.excerpt}</p>
                  </li>
                ))}
              </ol>
            </>
          )}
        </div>
      )}
    </Panel>
  );
}

function KindBadge({ kind }: { kind: KBArticleKind }) {
  const { t } = useTranslation();
  return (
    <span className="ml-1 rounded bg-hover px-1.5 py-px align-middle text-[11.5px] text-ink-2">
      {kind === "qa" ? t("settings.knowledge.kindQa") : t("settings.knowledge.kindArticle")}
    </span>
  );
}

function Articles() {
  const { t } = useTranslation();
  const articles = useKBArticles();
  const topics = useTopics();
  const [adding, setAdding] = useState<KBArticleKind | null>(null);
  const list = articles.data ?? [];
  return (
    <Panel title={t("settings.knowledge.articlesTitle")} lead={t("settings.knowledge.articlesLead")}>
      {!adding && articles.isSuccess && list.length < MAX_ARTICLES && (
        <div className="flex flex-wrap gap-2">
          <Button size="sm" onClick={() => setAdding("article")}>
            {t("settings.knowledge.addArticle")}
          </Button>
          <Button size="sm" onClick={() => setAdding("qa")}>
            {t("settings.knowledge.addQa")}
          </Button>
        </div>
      )}
      {adding && <ArticleForm kind={adding} topics={topics.data ?? []} onDone={() => setAdding(null)} />}
      {articles.isPending ? (
        <div role="status">
          <span className="sr-only">{t("app.loading")}</span>
          <Skeleton className="h-24" />
        </div>
      ) : articles.isError ? (
        <div role="alert" className="flex flex-col items-start gap-2">
          <p className="m-0 text-ink-2">{describeApiError(t, articles.error).message}</p>
          <Button onClick={() => void articles.refetch()}>{t("error.retry")}</Button>
        </div>
      ) : list.length === 0 && !adding ? (
        <p className="m-0 text-[13px] text-ink-3">{t("settings.knowledge.empty")}</p>
      ) : (
        <>
          <p className="m-0 text-[12.5px] text-ink-3">{t("settings.knowledge.count", { count: list.length, max: MAX_ARTICLES })}</p>
          <ul className="m-0 flex list-none flex-col gap-2 p-0">
            {list.map((a) => (
              <ArticleItem key={a.id} article={a} topics={topics.data ?? []} />
            ))}
          </ul>
        </>
      )}
    </Panel>
  );
}

function ArticleItem({ article, topics }: { article: KBArticle; topics: Topic[] }) {
  const { t } = useTranslation();
  const { remove } = useKBMutations();
  const [editing, setEditing] = useState(false);
  const [confirming, setConfirming] = useState(false);
  const full = useKBArticle(editing ? article.id : null);
  if (editing) {
    return (
      <li>
        {full.data ? (
          <ArticleForm kind={full.data.kind} article={full.data} topics={topics} onDone={() => setEditing(false)} />
        ) : full.isError ? (
          <p role="alert" className="m-0 text-[13px] text-danger">
            {describeApiError(t, full.error).message}
          </p>
        ) : (
          <div role="status">
            <span className="sr-only">{t("app.loading")}</span>
            <Skeleton className="h-40" />
          </div>
        )}
      </li>
    );
  }
  const names = article.topic_ids.map((id) => topics.find((x) => x.id === id)?.name).filter(Boolean);
  return (
    <li className="flex flex-wrap items-start gap-2 rounded-lg border border-line px-3 py-2 text-[13px]">
      <span className="flex min-w-0 flex-1 flex-col gap-0.5">
        <span className="font-medium">
          {article.title}
          <KindBadge kind={article.kind} />
          {!article.published && (
            <span className="ml-1 rounded bg-lamp-soft px-1.5 py-px align-middle text-[11.5px] text-ink">{t("settings.knowledge.draft")}</span>
          )}
        </span>
        <span className="line-clamp-2 text-[12.5px] text-ink-2">{article.body}</span>
        <span className="text-[12px] text-ink-3">{names.length > 0 ? names.join(", ") : t("settings.knowledge.allTopics")}</span>
      </span>
      {confirming ? (
        <span className="flex items-center gap-2">
          <span className="text-ink-2">{t("settings.knowledge.deleteConfirm")}</span>
          <Button size="sm" variant="destructive" disabled={remove.isPending} onClick={() => remove.mutate(article.id)}>
            {t("settings.knowledge.deleteSubmit")}
          </Button>
          <Button size="sm" onClick={() => setConfirming(false)}>
            {t("tasks.cancel")}
          </Button>
        </span>
      ) : (
        <span className="flex gap-1">
          <Button size="sm" variant="ghost" aria-label={t("settings.knowledge.editNamed", { title: article.title })} onClick={() => setEditing(true)}>
            {t("settings.knowledge.edit")}
          </Button>
          <Button
            size="sm"
            variant="ghost"
            className="text-danger"
            aria-label={t("settings.knowledge.deleteNamed", { title: article.title })}
            onClick={() => setConfirming(true)}
          >
            {t("settings.knowledge.delete")}
          </Button>
        </span>
      )}
      {remove.isError && (
        <p role="alert" className="m-0 w-full text-danger">
          {describeApiError(t, remove.error).message}
        </p>
      )}
    </li>
  );
}

function ArticleForm({ kind, article, topics, onDone }: { kind: KBArticleKind; article?: KBArticle; topics: Topic[]; onDone: () => void }) {
  const { t } = useTranslation();
  const { create, update } = useKBMutations();
  const save = article ? update : create;
  const key = article?.id ?? `new-${kind}`;
  const qa = kind === "qa";
  const [title, setTitle] = useState(article?.title ?? "");
  const [body, setBody] = useState(article?.body ?? "");
  const [url, setUrl] = useState(article?.url ?? "");
  const [published, setPublished] = useState(article?.published ?? true);
  const [topicIds, setTopicIds] = useState<string[]>(article?.topic_ids ?? []);
  const [errors, setErrors] = useState<{ title?: string; body?: string; url?: string }>({});

  const submit = (e: FormEvent) => {
    e.preventDefault();
    const found: typeof errors = {};
    if (!title.trim()) found.title = t(qa ? "settings.knowledge.questionRequired" : "settings.knowledge.titleRequired");
    if (!body.trim()) found.body = t(qa ? "settings.knowledge.answerRequired" : "settings.knowledge.bodyRequired");
    if (url.trim() && !/^https?:\/\/\S+$/.test(url.trim())) found.url = t("settings.knowledge.urlInvalid");
    setErrors(found);
    const first = (["title", "body", "url"] as const).find((f) => found[f]);
    if (first) {
      document.getElementById(`kb-${first}-${key}`)?.focus();
      return;
    }
    const input = { kind, title: title.trim(), body: body.trim(), url: url.trim() || null, published, topic_ids: topicIds };
    if (article) update.mutate({ id: article.id, body: input }, { onSuccess: onDone });
    else create.mutate(input, { onSuccess: onDone });
  };

  return (
    <form noValidate onSubmit={submit} className="flex flex-col gap-3 rounded-lg border border-teal bg-surface p-3">
      <TextField
        id={`kb-title-${key}`}
        name="title"
        label={t(qa ? "settings.knowledge.question" : "settings.knowledge.articleTitle")}
        hint={qa ? t("settings.knowledge.questionHint") : undefined}
        value={title}
        maxLength={MAX_TITLE}
        error={errors.title}
        onChange={(e) => setTitle(e.currentTarget.value.replace(/[\r\n]+/g, " "))}
      />
      <TextAreaField
        id={`kb-body-${key}`}
        name="body"
        label={t(qa ? "settings.knowledge.answer" : "settings.knowledge.body")}
        hint={t("settings.knowledge.bodyHint")}
        value={body}
        rows={qa ? 5 : 12}
        maxLength={MAX_BODY}
        error={errors.body}
        onChange={(e) => setBody(e.currentTarget.value)}
      />
      <TextField
        id={`kb-url-${key}`}
        name="url"
        type="url"
        label={t("settings.knowledge.url")}
        hint={t("settings.knowledge.urlHint")}
        value={url}
        maxLength={MAX_URL}
        error={errors.url}
        onChange={(e) => setUrl(e.currentTarget.value)}
      />
      {topics.length > 0 && (
        <fieldset className="m-0 flex flex-col gap-1 border-0 p-0">
          <legend className="mb-1 p-0 text-[13px] font-medium">{t("settings.knowledge.topics")}</legend>
          <p className="m-0 mb-1 text-[12.5px] text-ink-2">{t("settings.knowledge.topicsHint")}</p>
          {topics.map((x) => (
            <label key={x.id} className="flex items-center gap-2 text-[13px]">
              <input
                type="checkbox"
                checked={topicIds.includes(x.id)}
                onChange={(e) => {
                  const on = e.currentTarget.checked;
                  setTopicIds((ids) => (on ? [...ids, x.id] : ids.filter((id) => id !== x.id)));
                }}
                className="size-4 accent-teal"
              />
              {x.name}
            </label>
          ))}
        </fieldset>
      )}
      <label className="flex items-center gap-2 text-[13px]">
        <input type="checkbox" checked={published} onChange={(e) => setPublished(e.currentTarget.checked)} className="size-4 accent-teal" />
        {t("settings.knowledge.published")}
      </label>
      <FormMessage tone="error" message={save.isError ? describeApiError(t, save.error).message : null} />
      <div className="flex gap-2">
        <Button type="submit" size="sm" variant="primary" disabled={save.isPending}>
          {t("settings.knowledge.save")}
        </Button>
        <Button type="button" size="sm" onClick={onDone}>
          {t("tasks.cancel")}
        </Button>
      </div>
    </form>
  );
}
