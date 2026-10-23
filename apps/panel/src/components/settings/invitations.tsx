import { isApiError, type Invitation, type InvitationSent } from "@tolkovo/api-client";
import {
  useCreateInvitation,
  useInvitations,
  useResendInvitation,
  useRevokeInvitation,
} from "@tolkovo/api-client/react";
import { Button, Input, Label, Skeleton } from "@tolkovo/ui";
import { useEffect, useId, useRef, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";

import { describeApiError } from "../../lib/api-errors";
import { focusFirstInvalid, formValues, inviteSchema, validateForm, type FieldErrors } from "../../lib/forms";
import { FormMessage, SelectField, TextField } from "../form/fields";

/** A link to pass on by hand, shown once when the server cannot send email. */
type ManualLink = { email: string; url: string };

/**
 * Inviting people and managing open invitations (docs/06 A-03), for `members.manage`. With
 * outgoing email the server mails the link; without it the link is shown here once.
 */
export function Invitations() {
  const { t } = useTranslation();
  const [link, setLink] = useState<ManualLink | null>(null);
  const [notice, setNotice] = useState<string | null>(null);

  const onSent = (sent: InvitationSent, message: string) => {
    if (sent.invite_url) {
      setLink({ email: sent.invitation.email, url: sent.invite_url });
      setNotice(null);
    } else {
      setLink(null);
      setNotice(message);
    }
  };

  return (
    <div className="flex flex-col gap-6">
      <InviteForm
        onSent={(sent) => onSent(sent, t("settings.team.invite.sent", { email: sent.invitation.email }))}
        onStart={() => {
          setLink(null);
          setNotice(null);
        }}
      />
      <FormMessage tone="success" message={notice} />
      {link ? <LinkBox link={link} /> : null}
      <OpenInvitations
        onResent={(sent) => onSent(sent, t("settings.team.invitations.resent", { email: sent.invitation.email }))}
        onRevoked={(email) => {
          setLink(null);
          setNotice(t("settings.team.invitations.revoked", { email }));
        }}
      />
    </div>
  );
}

type Field = "email" | "role";

function InviteForm({ onSent, onStart }: { onSent: (sent: InvitationSent) => void; onStart: () => void }) {
  const { t } = useTranslation();
  const create = useCreateInvitation();
  const [fieldErrors, setFieldErrors] = useState<FieldErrors<Field>>({});
  const [serverError, setServerError] = useState<{ field?: Field; message: string } | null>(null);
  const formRef = useRef<HTMLFormElement>(null);

  // A field-bound server error takes the focus once the fieldset is enabled again.
  useEffect(() => {
    if (serverError?.field && !create.isPending && formRef.current) {
      focusFirstInvalid(formRef.current, [serverError.field]);
    }
  }, [serverError, create.isPending]);

  const onSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const form = event.currentTarget;
    onStart();
    setServerError(null);
    const result = validateForm(inviteSchema, formValues(form));
    if (!result.ok) {
      setFieldErrors(result.errors);
      focusFirstInvalid(form, Object.keys(result.errors));
      return;
    }
    setFieldErrors({});
    create.mutate(result.data, {
      onSuccess: (sent) => {
        form.reset();
        onSent(sent);
      },
      onError: (error) => setServerError(describeApiError<Field>(t, error, { already_member: "email" })),
    });
  };

  const fieldError = (field: Field) => {
    if (serverError?.field === field) return serverError.message;
    const key = fieldErrors[field];
    return key ? t(`validation.${key}`, { max: 254 }) : undefined;
  };

  return (
    <section aria-labelledby="settings-invite-title" className="flex max-w-[640px] flex-col gap-3">
      <h3 id="settings-invite-title" className="m-0 text-sm font-semibold">
        {t("settings.team.invite.title")}
      </h3>
      <form ref={formRef} onSubmit={onSubmit} noValidate aria-busy={create.isPending} className="flex flex-col gap-3">
        <FormMessage tone="error" message={serverError && !serverError.field ? serverError.message : null} />
        <fieldset disabled={create.isPending} className="m-0 flex flex-col gap-3 border-0 p-0">
          <div className="grid grid-cols-[minmax(0,1fr)_minmax(0,12rem)] gap-3 max-nav:grid-cols-1">
            <TextField
              id="invite-email"
              name="email"
              type="email"
              label={t("settings.team.invite.email")}
              autoComplete="off"
              inputMode="email"
              required
              error={fieldError("email")}
            />
            <SelectField id="invite-role" name="role" label={t("settings.team.invite.role")} defaultValue="operator">
              <option value="operator">{t("roles.operator")}</option>
              <option value="programmer">{t("roles.programmer")}</option>
              <option value="admin">{t("roles.admin")}</option>
            </SelectField>
          </div>
          <p className="m-0 text-[12.5px] text-ink-3">{t("settings.team.invite.roleHint")}</p>
        </fieldset>
        <div>
          <Button type="submit" variant="primary" disabled={create.isPending}>
            {create.isPending ? t("settings.team.invite.pending") : t("settings.team.invite.submit")}
          </Button>
        </div>
      </form>
    </section>
  );
}

function LinkBox({ link }: { link: ManualLink }) {
  const { t } = useTranslation();
  const id = useId();
  const input = useRef<HTMLInputElement>(null);
  const [copied, setCopied] = useState(false);

  const copy = () => {
    // Clipboard access may be refused; the selected text can still be copied by hand.
    input.current?.select();
    void navigator.clipboard?.writeText(link.url).then(
      () => setCopied(true),
      () => setCopied(false),
    );
  };

  return (
    <div className="flex max-w-[640px] flex-col gap-2 rounded-xl border border-lamp bg-lamp-soft p-3">
      <p className="m-0 text-[13px] text-ink">{t("settings.team.invite.link", { email: link.email })}</p>
      <Label htmlFor={id} className="sr-only">
        {t("settings.team.invite.linkLabel")}
      </Label>
      <div className="flex gap-2 max-nav:flex-col">
        <Input
          id={id}
          ref={input}
          value={link.url}
          readOnly
          onFocus={(e) => e.currentTarget.select()}
          className="min-w-0 flex-1 font-mono text-[12.5px]"
        />
        <Button variant="secondary" onClick={copy}>
          {t("settings.team.invite.copy")}
        </Button>
      </div>
      <FormMessage tone="success" message={copied ? t("settings.team.invite.copied") : null} />
    </div>
  );
}

type OpenInvitationsProps = {
  onResent: (sent: InvitationSent) => void;
  onRevoked: (email: string) => void;
};

function OpenInvitations({ onResent, onRevoked }: OpenInvitationsProps) {
  const { t } = useTranslation();
  const invitations = useInvitations();
  const resend = useResendInvitation();
  const revoke = useRevokeInvitation();
  const [error, setError] = useState<string | null>(null);

  const onError = (e: unknown) =>
    // 404: accepted or revoked a moment ago; the list refetches anyway.
    setError(isApiError(e) && e.code === "not_found" ? t("errors.invalidToken") : describeApiError(t, e).message);

  if (invitations.isPending) {
    return (
      <div role="status" className="flex max-w-[640px] flex-col gap-2">
        <span className="sr-only">{t("app.loading")}</span>
        <Skeleton className="h-12" />
      </div>
    );
  }
  if (invitations.isError) {
    return (
      <div role="alert" className="flex flex-col items-start gap-2">
        <p className="m-0 text-ink-2">{describeApiError(t, invitations.error).message}</p>
        <Button variant="secondary" onClick={() => void invitations.refetch()}>
          {t("error.retry")}
        </Button>
      </div>
    );
  }
  if (invitations.data.items.length === 0) return null;

  const busy = resend.isPending || revoke.isPending;
  const emailEnabled = invitations.data.email_enabled;
  return (
    <section aria-labelledby="settings-invitations-title" className="flex max-w-[640px] flex-col gap-3">
      <h3 id="settings-invitations-title" className="m-0 text-sm font-semibold">
        {t("settings.team.invitations.title")}
      </h3>
      <FormMessage tone="error" message={error} />
      <ul
        aria-labelledby="settings-invitations-title"
        className="m-0 flex list-none flex-col divide-y divide-line rounded-xl border border-line bg-surface p-0"
      >
        {invitations.data.items.map((inv) => (
          <InvitationRow
            key={inv.id}
            invitation={inv}
            emailEnabled={emailEnabled}
            disabled={busy}
            onResend={() => {
              setError(null);
              resend.mutate(inv.id, { onSuccess: onResent, onError });
            }}
            onRevoke={() => {
              setError(null);
              revoke.mutate(inv.id, { onSuccess: () => onRevoked(inv.email), onError });
            }}
          />
        ))}
      </ul>
    </section>
  );
}

type InvitationRowProps = {
  invitation: Invitation;
  emailEnabled: boolean;
  disabled: boolean;
  onResend: () => void;
  onRevoke: () => void;
};

function InvitationRow({ invitation, emailEnabled, disabled, onResend, onRevoke }: InvitationRowProps) {
  const { t, i18n } = useTranslation();
  const date = new Intl.DateTimeFormat(i18n.language, { dateStyle: "medium" }).format(new Date(invitation.expires_at));
  const email = invitation.email;
  return (
    <li className="flex items-center gap-3 px-3 py-2.5 max-nav:flex-wrap">
      <div className="flex min-w-0 flex-1 flex-col gap-0.5">
        <p className="m-0 flex flex-wrap items-center gap-2 text-[13px] font-medium text-ink">
          <span className="truncate">{email}</span>
          <span className="text-ink-2">{t(`roles.${invitation.role}`)}</span>
        </p>
        <p className="m-0 flex flex-wrap gap-x-3 text-[12.5px] text-ink-3">
          {invitation.expired ? (
            <span className="font-medium text-danger">{t("settings.team.invitations.expired")}</span>
          ) : (
            <span>{t("settings.team.invitations.expires", { date })}</span>
          )}
          {invitation.invited_by_name ? (
            <span>{t("settings.team.invitations.by", { name: invitation.invited_by_name })}</span>
          ) : null}
        </p>
      </div>
      <div className="flex gap-1">
        <Button
          variant="ghost"
          size="sm"
          disabled={disabled}
          aria-label={
            emailEnabled
              ? t("settings.team.invitations.resendLabel", { email })
              : t("settings.team.invitations.newLinkLabel", { email })
          }
          onClick={onResend}
        >
          {emailEnabled ? t("settings.team.invitations.resend") : t("settings.team.invitations.newLink")}
        </Button>
        <Button
          variant="ghost"
          size="sm"
          disabled={disabled}
          aria-label={t("settings.team.invitations.revokeLabel", { email })}
          onClick={onRevoke}
        >
          {t("settings.team.invitations.revoke")}
        </Button>
      </div>
    </li>
  );
}
