import { createFileRoute, redirect } from "@tanstack/react-router";

export const Route = createFileRoute("/")({
  beforeLoad: () => {
    // The inbox is the operator's home screen (docs/04 §2.1).
    throw redirect({ to: "/inbox", replace: true });
  },
});
