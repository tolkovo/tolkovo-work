import { parseAnswer, unwrap, type ApiClient } from "./client";
import type { components } from "./schema.gen";
import { accessTokenCreatedSchema, accessTokenListSchema } from "./schemas";

/** Personal access tokens (ADR-0016): scripts and AI agents work with tasks on a member's behalf. */

type Schemas = components["schemas"];

export type AccessToken = Schemas["AccessToken"];
export type AccessTokenScope = Schemas["AccessTokenScope"];
export type AccessTokenCreate = Schemas["AccessTokenCreate"];
export type AccessTokenCreated = Schemas["AccessTokenCreated"];

export async function fetchMyAccessTokens(client: ApiClient): Promise<AccessToken[]> {
  return parseAnswer(accessTokenListSchema, await unwrap(client.GET("/me/access-tokens"))).items;
}

export async function createAccessToken(client: ApiClient, body: AccessTokenCreate): Promise<AccessTokenCreated> {
  return parseAnswer(accessTokenCreatedSchema, await unwrap(client.POST("/me/access-tokens", { body })));
}

export async function revokeMyAccessToken(client: ApiClient, id: string): Promise<void> {
  await unwrap(client.DELETE("/me/access-tokens/{id}", { params: { path: { id } } }));
}

/** Every member's tokens; `members.manage`. */
export async function fetchAccessTokens(client: ApiClient): Promise<AccessToken[]> {
  return parseAnswer(accessTokenListSchema, await unwrap(client.GET("/access-tokens"))).items;
}

export async function revokeAccessToken(client: ApiClient, id: string): Promise<void> {
  await unwrap(client.DELETE("/access-tokens/{id}", { params: { path: { id } } }));
}
