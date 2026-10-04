/**
 * Where the panel connects to the Tolkovo REST API.
 *
 * The typed client will come from `packages/api-client`, generated from `api/openapi.yaml`
 * (docs/00 §7.1, §8). It is created here once, from this base URL, and passed to queries
 * through the router context next to the QueryClient. Responses are validated with zod at
 * this boundary. Until the contract exists this module holds only the base path, so no
 * hand-written backend types appear in the panel.
 *
 * In development Vite proxies /api to the Go server on http://localhost:8080 (vite.config.ts).
 */
export const API_BASE_URL = "/api/v1";
