import { describe, expect, it } from "vitest";

import { gitHubReturn, parseLabels, validateIntegrationsSearch } from "./github";

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
});
