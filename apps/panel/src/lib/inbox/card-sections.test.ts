import { afterEach, describe, expect, it, vi } from "vitest";

import { CARD_SECTIONS_KEY, readCollapsed } from "./card-sections";

describe("readCollapsed", () => {
  afterEach(() => {
    window.localStorage.clear();
    vi.restoreAllMocks();
  });

  it("folds technical details until the operator decides", () => {
    expect(readCollapsed()).toEqual(["tech"]);
  });

  it("reads the saved choice and ignores anything else in storage", () => {
    window.localStorage.setItem(CARD_SECTIONS_KEY, JSON.stringify(["history"]));
    expect(readCollapsed()).toEqual(["history"]);
    window.localStorage.setItem(CARD_SECTIONS_KEY, JSON.stringify(["history", "secrets"]));
    expect(readCollapsed()).toEqual(["tech"]);
    window.localStorage.setItem(CARD_SECTIONS_KEY, "{not json");
    expect(readCollapsed()).toEqual(["tech"]);
  });

  it("works without storage", () => {
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new Error("blocked");
    });
    expect(readCollapsed()).toEqual(["tech"]);
  });
});
