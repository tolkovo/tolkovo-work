import { createFileRoute } from "@tanstack/react-router";
import { Input, Label } from "@tolkovo/ui";
import { useTranslation } from "react-i18next";

import { AuthCard, Field } from "../../components/auth/auth-card";

export const Route = createFileRoute("/_auth/login")({
  component: LoginPage,
});

function LoginPage() {
  const { t } = useTranslation();
  return (
    <AuthCard
      title={t("auth.login.title")}
      subtitle={t("auth.login.subtitle")}
      submitLabel={t("auth.login.submit")}
      switchPrompt={t("auth.login.switchPrompt")}
      switchAction={t("auth.login.switchAction")}
      switchTo="/register"
    >
      <Field>
        <Label htmlFor="login-email">{t("auth.email")}</Label>
        <Input id="login-email" name="email" type="email" autoComplete="username" inputMode="email" required />
      </Field>
      <Field>
        <Label htmlFor="login-password">{t("auth.password")}</Label>
        <Input id="login-password" name="password" type="password" autoComplete="current-password" required />
      </Field>
    </AuthCard>
  );
}
