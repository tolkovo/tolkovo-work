import type { Contact, ContactInput, Conversation } from "@tolkovo/api-client";
import { useCan, useContact, useConversationList, useUpdateContact } from "@tolkovo/api-client/react";
import { useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { Button, Skeleton, cn } from "@tolkovo/ui";
import { ChevronRight, Copy, Pencil, X } from "lucide-react";
import { useId, useState, type FormEvent, type ReactNode, type Ref } from "react";
import { useTranslation } from "react-i18next";

import { describeApiError } from "../../lib/api-errors";
import { focusFirstInvalid, formValues, contactSchema, validateForm, type FieldErrors } from "../../lib/forms";
import { queriesShowingContact } from "../../lib/inbox/cache";
import { useCardSections, type CardSection } from "../../lib/inbox/card-sections";
import { initials, type InboxTab } from "../../lib/inbox/tabs";
import { FormMessage, SelectField, TextField } from "../form/fields";

export const CUSTOMER_CARD_ID = "customer-card";

type CustomerCardProps = {
  conversation: Conversation;
  tab: InboxTab;
  /** Below 1240 px the card is a drawer over the conversation (docs/07 §4); wider it is a column. */
  open: boolean;
  onClose: () => void;
  headingRef: Ref<HTMLHeadingElement>;
};

/**
 * The customer card, the third column of the inbox (docs/07 §4): who this is, their earlier
 * conversations, technical details. Channels, local time, the AI summary and custom fields
 * arrive with the tasks that bring the data (docs/06 I-03).
 */
export function CustomerCard({ conversation: c, tab, open, onClose, headingRef }: CustomerCardProps) {
  const { t } = useTranslation();
  const contact = useContact(c.contact.id);
  return (
    <>
      {open && (
        // Below 1240 px a click beside the drawer closes it, like in the prototype.
        <button
          type="button"
          tabIndex={-1}
          aria-label={t("inbox.card.close")}
          onClick={onClose}
          className="fixed inset-0 z-30 bg-ink/20 wide:hidden"
        />
      )}
      <aside
        id={CUSTOMER_CARD_ID}
        aria-labelledby="customer-card-title"
        onKeyDown={(e) => {
          if (e.key === "Escape" && open) {
            e.stopPropagation();
            onClose();
          }
        }}
        className={cn(
          "flex w-[300px] shrink-0 flex-col overflow-auto border-l border-line bg-surface",
          open
            ? "max-wide:fixed max-wide:inset-y-0 max-wide:right-0 max-wide:z-40 max-wide:w-[min(340px,100%)] max-wide:shadow-pop"
            : "max-wide:hidden",
        )}
      >
        <header className="flex items-start gap-3 border-b border-line px-4 py-3.5">
          <span
            aria-hidden="true"
            className="grid size-10 shrink-0 place-items-center rounded-full bg-teal-soft text-sm font-semibold text-teal"
          >
            {initials(contact.data?.name ?? c.contact.name)}
          </span>
          <div className="min-w-0 flex-1">
            <h2
              id="customer-card-title"
              ref={headingRef}
              tabIndex={-1}
              className="m-0 truncate text-[15px] font-semibold outline-none"
            >
              {(contact.data?.name ?? c.contact.name) || t("inbox.contact")}
            </h2>
            {contact.data && <Since contact={contact.data} />}
          </div>
          <button
            type="button"
            aria-label={t("inbox.card.close")}
            onClick={onClose}
            className="grid size-8 shrink-0 place-items-center rounded-md text-ink-2 hover:bg-hover wide:hidden"
          >
            <X aria-hidden="true" className="size-4" />
          </button>
        </header>
        <Section id="who" title={t("inbox.card.section.who")}>
          {contact.isPending ? (
            <Skeleton className="h-16" />
          ) : contact.isError ? (
            <p role="alert" className="m-0 flex flex-col items-start gap-2 text-[13px] text-ink-2">
              {t("inbox.card.loadError")}
              <Button size="sm" onClick={() => void contact.refetch()}>
                {t("inbox.retry")}
              </Button>
            </p>
          ) : (
            <Who contact={contact.data} />
          )}
        </Section>
        <History conversation={c} tab={tab} />
        <Section id="tech" title={t("inbox.card.section.tech")}>
          <TechDetails contactId={c.contact.id} />
        </Section>
      </aside>
    </>
  );
}

function Since({ contact }: { contact: Contact }) {
  const { t, i18n } = useTranslation();
  const date = new Intl.DateTimeFormat(i18n.language, { dateStyle: "long" }).format(new Date(contact.created_at));
  return <p className="m-0 mt-0.5 text-xs text-ink-3">{t("inbox.card.since", { date })}</p>;
}

function Section({ id, title, extra, children }: { id: CardSection; title: string; extra?: ReactNode; children: ReactNode }) {
  const collapsed = useCardSections((s) => s.collapsed.includes(id));
  const toggle = useCardSections((s) => s.toggle);
  const bodyId = useId();
  return (
    <section className="border-b border-line">
      <h3 className="m-0">
        <button
          type="button"
          aria-expanded={!collapsed}
          aria-controls={bodyId}
          onClick={() => toggle(id)}
          className="flex w-full items-center gap-1.5 px-4 py-2.5 text-left text-[11px] font-semibold tracking-[0.06em] text-ink-3 uppercase hover:text-ink"
        >
          <ChevronRight aria-hidden="true" className={cn("size-3.5 transition-transform", !collapsed && "rotate-90")} />
          {title}
          {extra !== undefined && <span className="ml-auto font-normal tracking-normal normal-case">{extra}</span>}
        </button>
      </h3>
      <div id={bodyId} hidden={collapsed} className="px-4 pb-3.5">
        {children}
      </div>
    </section>
  );
}

function Who({ contact }: { contact: Contact }) {
  const { t } = useTranslation();
  const canEdit = useCan("contacts.edit");
  const [editing, setEditing] = useState(false);
  if (editing) return <ContactForm contact={contact} onDone={() => setEditing(false)} />;

  const empty = (
    <>
      <span aria-hidden="true">—</span>
      <span className="sr-only">{t("inbox.card.empty")}</span>
    </>
  );
  return (
    <div className="flex flex-col items-start gap-2.5">
      <dl className="m-0 grid w-full grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-1.5 text-[13px]">
        <dt className="text-ink-3">{t("inbox.card.email")}</dt>
        <dd className="m-0 break-all">{contact.email ?? empty}</dd>
        <dt className="text-ink-3">{t("inbox.card.phone")}</dt>
        <dd className="m-0 font-mono text-[12.5px]">{contact.phone ?? empty}</dd>
        <dt className="text-ink-3">{t("inbox.card.language")}</dt>
        <dd className="m-0">{contact.locale ? t(contact.locale === "en" ? "inbox.card.languages.en" : "inbox.card.languages.ru") : empty}</dd>
      </dl>
      {canEdit && (
        <Button variant="ghost" size="sm" className="-ml-2.5" onClick={() => setEditing(true)}>
          <Pencil aria-hidden="true" />
          {t("inbox.card.edit")}
        </Button>
      )}
    </div>
  );
}

type Field = "name" | "email" | "phone" | "locale";

/** Edits the contact in place; only changed fields go to the server (PATCH /contacts/{id}). */
function ContactForm({ contact, onDone }: { contact: Contact; onDone: () => void }) {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const update = useUpdateContact(contact.id);
  const [fieldErrors, setFieldErrors] = useState<FieldErrors<Field>>({});
  const id = useId();

  const onSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const form = event.currentTarget;
    const result = validateForm(contactSchema, formValues(form));
    if (!result.ok) {
      setFieldErrors(result.errors);
      focusFirstInvalid(form, Object.keys(result.errors));
      return;
    }
    setFieldErrors({});
    const was = { name: contact.name, email: contact.email ?? "", phone: contact.phone ?? "", locale: contact.locale ?? "" };
    const body: ContactInput = {};
    for (const field of ["name", "email", "phone", "locale"] as const) {
      if (result.data[field] !== was[field]) body[field] = result.data[field];
    }
    if (Object.keys(body).length === 0) {
      onDone();
      return;
    }
    update.mutate(body, {
      onSuccess: () => {
        // The name shows in the inbox rows and the conversation header.
        if (body.name !== undefined) {
          for (const queryKey of queriesShowingContact(queryClient, contact.id)) {
            void queryClient.invalidateQueries({ queryKey });
          }
        }
        onDone();
      },
    });
  };

  const fieldError = (field: Field) => {
    const key = fieldErrors[field];
    return key ? t(`validation.${key}`, { max: field === "name" ? 200 : 254 }) : undefined;
  };

  return (
    <form onSubmit={onSubmit} noValidate aria-busy={update.isPending} className="flex flex-col gap-3">
      <FormMessage tone="error" message={update.isError ? describeApiError(t, update.error).message : null} />
      <fieldset disabled={update.isPending} className="m-0 flex flex-col gap-3 border-0 p-0">
        <TextField
          id={`${id}-name`}
          name="name"
          label={t("inbox.card.name")}
          defaultValue={contact.name}
          maxLength={200}
          autoComplete="off"
          error={fieldError("name")}
        />
        <TextField
          id={`${id}-email`}
          name="email"
          type="email"
          label={t("inbox.card.email")}
          defaultValue={contact.email ?? ""}
          maxLength={254}
          autoComplete="off"
          error={fieldError("email")}
        />
        <TextField
          id={`${id}-phone`}
          name="phone"
          type="tel"
          label={t("inbox.card.phone")}
          defaultValue={contact.phone ?? ""}
          maxLength={32}
          autoComplete="off"
          error={fieldError("phone")}
        />
        <SelectField
          id={`${id}-locale`}
          name="locale"
          label={t("inbox.card.language")}
          defaultValue={contact.locale ?? ""}
          error={fieldError("locale")}
        >
          <option value="">{t("inbox.card.languages.none")}</option>
          <option value="ru" lang="ru">
            {t("inbox.card.languages.ru")}
          </option>
          <option value="en" lang="en">
            {t("inbox.card.languages.en")}
          </option>
        </SelectField>
      </fieldset>
      <div className="flex gap-2">
        <Button type="submit" variant="primary" size="sm" disabled={update.isPending}>
          {update.isPending ? t("inbox.card.saving") : t("inbox.card.save")}
        </Button>
        <Button type="button" variant="ghost" size="sm" disabled={update.isPending} onClick={onDone}>
          {t("inbox.card.cancel")}
        </Button>
      </div>
    </form>
  );
}

/** Earlier and later conversations of the same customer, newest activity first (GET /conversations?contact_id=). */
function History({ conversation: c, tab }: { conversation: Conversation; tab: InboxTab }) {
  const { t, i18n } = useTranslation();
  const history = useConversationList({ contact_id: c.contact.id });
  const items = history.data?.pages.flatMap((p) => p.items) ?? [];
  const thisYear = new Date().getFullYear();
  const date = (iso: string) => {
    const at = new Date(iso);
    return new Intl.DateTimeFormat(i18n.language, {
      day: "numeric",
      month: "short",
      ...(at.getFullYear() === thisYear ? {} : { year: "numeric" }),
    }).format(at);
  };
  const count = history.isSuccess ? `${items.length}${history.hasNextPage ? "+" : ""}` : undefined;

  return (
    <Section
      id="history"
      title={t("inbox.card.section.history")}
      extra={count && <span aria-label={t("inbox.card.history.count", { count: items.length })}>{count}</span>}
    >
      {history.isPending && <Skeleton className="h-12" />}
      {history.isError && (
        <p role="alert" className="m-0 flex flex-col items-start gap-2 text-[13px] text-ink-2">
          {t("inbox.card.history.error")}
          <Button size="sm" onClick={() => void history.refetch()}>
            {t("inbox.retry")}
          </Button>
        </p>
      )}
      {history.isSuccess && (
        <ol className="m-0 -mx-2 flex list-none flex-col gap-0.5 p-0">
          {items.map((h) => {
            const current = h.id === c.id;
            const about = h.subject ?? h.last_message?.text ?? "";
            return (
              <li key={h.id}>
                <Link
                  to="/inbox/$conversationId"
                  params={{ conversationId: h.id }}
                  search={{ tab }}
                  aria-current={current ? "page" : undefined}
                  className="flex flex-col gap-0.5 rounded-md px-2 py-1.5 hover:bg-hover aria-[current=page]:bg-sel"
                >
                  <span className="flex items-center gap-2 text-[13px]">
                    <span className="font-mono text-[11px] text-ink-3">{t("inbox.number", { number: h.number })}</span>
                    <span className="font-medium">{t(`inbox.status.${h.status}`)}</span>
                    <time dateTime={h.created_at} className="ml-auto text-xs text-ink-3">
                      {date(h.created_at)}
                    </time>
                  </span>
                  <span className="truncate text-xs text-ink-2">
                    {current ? t("inbox.card.history.current") : about || t("inbox.noPreview")}
                  </span>
                </Link>
              </li>
            );
          })}
        </ol>
      )}
      {history.isSuccess && items.every((h) => h.id === c.id) && (
        <p className="m-0 mt-1.5 text-[13px] text-ink-3">{t("inbox.card.history.empty")}</p>
      )}
      {history.hasNextPage && (
        <Button
          variant="ghost"
          size="sm"
          className="-ml-2.5 mt-1"
          disabled={history.isFetchingNextPage}
          onClick={() => void history.fetchNextPage()}
        >
          {t("inbox.card.history.more")}
        </Button>
      )}
    </Section>
  );
}

function TechDetails({ contactId }: { contactId: string }) {
  const { t } = useTranslation();
  const [copied, setCopied] = useState(false);
  const copy = () => {
    void navigator.clipboard?.writeText(contactId).then(
      () => setCopied(true),
      () => setCopied(false),
    );
  };
  return (
    <dl className="m-0 flex flex-col gap-1 text-[13px]">
      <dt className="text-ink-3">{t("inbox.card.contactId")}</dt>
      <dd className="m-0 flex items-center gap-1">
        <code className="min-w-0 flex-1 font-mono text-[11.5px] break-all text-ink-2 select-all">{contactId}</code>
        <button
          type="button"
          onClick={copy}
          aria-label={t("inbox.card.copy")}
          className="grid size-7 shrink-0 place-items-center rounded-md text-ink-3 hover:bg-hover hover:text-ink"
        >
          <Copy aria-hidden="true" className="size-3.5" />
        </button>
        <span role="status" className={copied ? "text-xs text-teal" : "sr-only"}>
          {copied ? t("inbox.card.copied") : ""}
        </span>
      </dd>
    </dl>
  );
}
