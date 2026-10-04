/**
 * Where GitHub sends the browser back during the connection (GH-01, ADR-0012):
 * `?github=created&code=…&state=…` after the App is created, `?github=installed&installation_id=…`
 * after it is installed. Anything else on /settings/integrations is dropped.
 */
export type GitHubReturn =
  | { github: "created"; code: string; state: string }
  | { github: "installed"; installation_id: number };

export type IntegrationsSearch = Partial<{
  github: "created" | "installed";
  code: string;
  state: string;
  installation_id: number;
}>;

export function validateIntegrationsSearch(search: Record<string, unknown>): IntegrationsSearch {
  const str = (v: unknown) => (typeof v === "string" && v.length > 0 && v.length <= 200 ? v : undefined);
  const id = typeof search.installation_id === "number" ? search.installation_id : Number(search.installation_id);
  const out: IntegrationsSearch = {};
  if (search.github === "created" || search.github === "installed") out.github = search.github;
  const code = str(search.code);
  const state = str(search.state);
  if (code) out.code = code;
  if (state) out.state = state;
  if (Number.isSafeInteger(id) && id > 0) out.installation_id = id;
  return out;
}

/** The step GitHub returned from, if the search holds a complete one. */
export function gitHubReturn(search: IntegrationsSearch): GitHubReturn | null {
  if (search.github === "created" && search.code && search.state) {
    return { github: "created", code: search.code, state: search.state };
  }
  if (search.github === "installed" && search.installation_id) {
    return { github: "installed", installation_id: search.installation_id };
  }
  return null;
}

/**
 * Sends the browser to GitHub with a POST form, the way the manifest flow requires: GitHub reads
 * the App's description from the form field `manifest`.
 */
export function postToGitHub(action: string, manifest: string): void {
  const form = document.createElement("form");
  form.method = "post";
  form.action = action;
  form.hidden = true;
  const input = document.createElement("input");
  input.type = "hidden";
  input.name = "manifest";
  input.value = manifest;
  form.append(input);
  document.body.append(form);
  form.submit();
}

/** "bug, from-support" → ["bug", "from-support"]. The server trims and drops repeats too. */
export function parseLabels(text: string): string[] {
  return text
    .split(",")
    .map((l) => l.trim())
    .filter(Boolean);
}
