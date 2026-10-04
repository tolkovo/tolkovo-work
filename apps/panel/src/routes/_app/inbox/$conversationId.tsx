import { createFileRoute } from "@tanstack/react-router";

import { ConversationView } from "../../../components/inbox/conversation-view";

export const Route = createFileRoute("/_app/inbox/$conversationId")({
  component: ConversationPage,
});

function ConversationPage() {
  const { conversationId } = Route.useParams();
  const { tab = "mine" } = Route.useSearch();
  // A new key per conversation: scroll position and pending state never leak between them.
  return <ConversationView key={conversationId} conversationId={conversationId} tab={tab} />;
}
