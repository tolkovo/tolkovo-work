import { describe, expect, it } from "vitest";

import { gitHubReturn, parseIssueNumber, parseLabels, validateIntegrationsSearch } from "./github";

describe("GitHub return", () => {
  it("keeps only the fields of a step", () => {
    expect(validateIntegrationsSearch({ github: "installed", installation_id: "8", setup_action: "install" })).toEqual({
      github: "installed",
      installation_id: 8,
    });
    expect(validateIntegrationsSearch({ github: "other", code: "", installation_id: -1 })).toEqual({});
    expect(validateIntegrationsSearch({ code: "x".repeat(201) })).toEqual({});
  });

  it("is a step only when complete", () => {
    expect(gitHubReturn({ github: "created", code: "c" })).toBeNull();
    expect(gitHubReturn({ github: "created", code: "c", state: "s" })).toEqual({ github: "created", code: "c", state: "s" });
    expect(gitHubReturn({ github: "installed" })).toBeNull();
    expect(gitHubReturn({ github: "installed", installation_id: 8 })).toEqual({ github: "installed", installation_id: 8 });
  });

  it("splits labels by commas", () => {
    expect(parseLabels(" bug, ,from-support ,")).toEqual(["bug", "from-support"]);
    expect(parseLabels("")).toEqual([]);
  });

  it("takes an issue number, #number or the issue's address", () => {
    expect(parseIssueNumber(" 12 ")).toBe(12);
    expect(parseIssueNumber("#7")).toBe(7);
    expect(parseIssueNumber("https://github.com/zerno-team/desktop/issues/31")).toBe(31);
    expect(parseIssueNumber("https://github.com/zerno-team/desktop/issues/31#issuecomment-1")).toBe(31);
    expect(parseIssueNumber("https://github.com/zerno-team/desktop/pull/31")).toBeNull();
    expect(parseIssueNumber("0")).toBeNull();
    expect(parseIssueNumber("abc")).toBeNull();
  });
});
