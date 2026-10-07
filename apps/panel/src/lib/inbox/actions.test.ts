import type { Session } from "@tolkovo/api-client";
import { describe, expect, it } from "vitest";

import { conv, inboxOperator } from "../../test/inbox-fixtures";
import { abilities } from "./actions";

const me = inboxOperator.user.id;
const admin: Session = {
  ...inboxOperator,
  role: "admin",
  permissions: [...inboxOperator.permissions, "conversations.assign", "conversations.manage", "tags.manage"],
};

// The rules of api/openapi.yaml and docs/04 §4.3 as the panel hides buttons by them.
describe("abilities", () => {
  it("lets an operator act on own conversations only", () => {
    const own = abilities(inboxOperator, conv("c", { assignee_id: me }));
    expect(own).toMatchObject({ reply: true, note: true, close: true, wait: true, transfer: true, release: true, take: false });
    const theirs = abilities(inboxOperator, conv("c", { assignee_id: "u-anna" }));
    expect(theirs).toMatchObject({ reply: false, note: true, close: false, transfer: false, release: false, tag: true });
    const queued = abilities(inboxOperator, conv("c", { status: "queued", assignee_id: null }));
    expect(queued).toMatchObject({ take: true, reply: false, close: false });
    // The AI bot answers it: an operator may take it over (ADR-0018).
    const bot = abilities(inboxOperator, conv("c", { status: "bot", assignee_id: null }));
    expect(bot).toMatchObject({ take: true, reply: false, close: false, release: false });
  });

  it("follows the state machine", () => {
    expect(abilities(inboxOperator, conv("c", { status: "pending" }))).toMatchObject({ resume: true, wait: false, release: false, close: true });
    expect(abilities(inboxOperator, conv("c", { status: "resolved" }))).toMatchObject({ reopen: true, close: false, transfer: false });
    expect(abilities(inboxOperator, conv("c", { status: "closed" }))).toMatchObject({ reply: false, note: false, reopen: false, tag: true });
  });

  it("widens to any conversation with manage and assign", () => {
    const theirs = abilities(admin, conv("c", { assignee_id: "u-anna" }));
    expect(theirs).toMatchObject({ reply: true, close: true, transfer: true, release: true, createTag: true });
    expect(abilities(inboxOperator, conv("c")).createTag).toBe(false);
  });

  it("gives nothing without a session", () => {
    expect(Object.values(abilities(null, conv("c"))).some(Boolean)).toBe(false);
  });
});
