import { parseAnswer, unwrap, type ApiClient } from "./client";
import type { components } from "./schema.gen";
import { kbArticleListSchema, kbArticleSchema, kbGuideSchema, kbImportResultSchema, kbSearchResultSchema } from "./schemas";

/** The knowledge base of the AI bot (ADR-0018); `knowledge.manage`. */

type Schemas = components["schemas"];

export type KBArticle = Schemas["KBArticle"];
export type KBArticleKind = Schemas["KBArticleKind"];
export type KBArticleInput = Schemas["KBArticleInput"];
export type KBHit = Schemas["KBHit"];
export type KBGuide = Schemas["KBGuide"];
export type KBGuideInput = Schemas["KBGuideInput"];
export type KBImport = Schemas["KBImport"];
export type KBImportResult = Schemas["KBImportResult"];

/** Every entry, the last changed first; `body` holds the first 300 characters. */
export async function fetchKBArticles(client: ApiClient): Promise<KBArticle[]> {
  return parseAnswer(kbArticleListSchema, await unwrap(client.GET("/kb/articles"))).items;
}

export async function fetchKBArticle(client: ApiClient, id: string): Promise<KBArticle> {
  return parseAnswer(kbArticleSchema, await unwrap(client.GET("/kb/articles/{id}", { params: { path: { id } } })));
}

export async function createKBArticle(client: ApiClient, body: KBArticleInput): Promise<KBArticle> {
  return parseAnswer(kbArticleSchema, await unwrap(client.POST("/kb/articles", { body })), 201);
}

export async function updateKBArticle(client: ApiClient, id: string, body: KBArticleInput): Promise<KBArticle> {
  return parseAnswer(kbArticleSchema, await unwrap(client.PUT("/kb/articles/{id}", { params: { path: { id } }, body })));
}

export async function deleteKBArticle(client: ApiClient, id: string): Promise<void> {
  await unwrap(client.DELETE("/kb/articles/{id}", { params: { path: { id } } }));
}

/** What the bot would find for a question; costs no tokens. */
export async function searchKB(client: ApiClient, q: string, topic?: string): Promise<KBHit[]> {
  const query = topic ? { q, topic } : { q };
  return parseAnswer(kbSearchResultSchema, await unwrap(client.GET("/kb/search", { params: { query } }))).items;
}

export async function fetchKBGuide(client: ApiClient): Promise<KBGuide> {
  return parseAnswer(kbGuideSchema, await unwrap(client.GET("/kb/guide")));
}

export async function setKBGuide(client: ApiClient, body: KBGuideInput): Promise<KBGuide> {
  return parseAnswer(kbGuideSchema, await unwrap(client.PUT("/kb/guide", { body })));
}

/** A knowledge file written by an AI agent (guides/knowledge-base-brief.md). */
export async function importKB(client: ApiClient, body: KBImport): Promise<KBImportResult> {
  return parseAnswer(kbImportResultSchema, await unwrap(client.POST("/kb/import", { body })));
}
