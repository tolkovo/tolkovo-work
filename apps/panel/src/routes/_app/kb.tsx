import { createFileRoute, redirect } from "@tanstack/react-router";

export const Route = createFileRoute("/_app/kb")({
  beforeLoad: () => {
    // The knowledge base lives in «Настройки → База знаний» (ADR-0018); the rail leads there.
    throw redirect({ to: "/settings/knowledge", replace: true });
  },
});
