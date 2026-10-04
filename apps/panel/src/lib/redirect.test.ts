import { describe, expect, it } from "vitest";

import { safeReturnPath } from "./redirect";

describe("safeReturnPath", () => {
  it.each(["/inbox", "/settings/team?tab=1", "/inbox#c1"])("keeps the panel path %j", (path) => {
    expect(safeReturnPath(path)).toBe(path);
  });

  it.each([
    "//evil.example/x",
    "https://evil.example",
    "/\\evil.example",
    "javascript:alert(1)",
    "/ /evil",
    "inbox",
    "/login?redirect=/inbox",
    "/register",
    42,
    undefined,
  ])("drops %j", (value) => {
    expect(safeReturnPath(value)).toBeNull();
  });
});
