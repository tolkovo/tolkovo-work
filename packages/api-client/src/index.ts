/**
 * Tolkovo REST client. Types are generated from api/openapi.yaml by openapi-typescript
 * (`make gen` → src/schema.gen.ts, committed and checked for staleness by `make web-check`);
 * never edit the generated file. React hooks live in `@tolkovo/api-client/react`, so the
 * widget can use the types and the client without React (docs/04 §1.2).
 */
export type { components, operations, paths } from "./schema.gen";
export { CSRF_HEADER, createApiClient, csrfMiddleware, parseAnswer, unwrap } from "./client";
export type { ApiClient, ApiClientOptions } from "./client";
export { ApiError, isApiError, type ApiErrorCode } from "./errors";
export * from "./endpoints";
export * from "./tasks";
