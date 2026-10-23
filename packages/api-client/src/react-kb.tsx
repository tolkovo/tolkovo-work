import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import {
  createKBArticle,
  deleteKBArticle,
  fetchKBArticle,
  fetchKBArticles,
  fetchKBGuide,
  importKB,
  searchKB,
  setKBGuide,
  updateKBArticle,
  type KBArticleInput,
  type KBGuideInput,
  type KBImport,
} from "./knowledge";
import { queryKeys, useApiClient } from "./react-core";

/** Hooks of the bot's knowledge base (ADR-0018). */

export function useKBArticles() {
  const client = useApiClient();
  return useQuery({ queryKey: queryKeys.kbArticles, queryFn: () => fetchKBArticles(client) });
}

/** One entry with its whole text; off while `id` is null. */
export function useKBArticle(id: string | null) {
  const client = useApiClient();
  return useQuery({ queryKey: queryKeys.kbArticle(id ?? ""), queryFn: () => fetchKBArticle(client, id!), enabled: id !== null });
}

/** What the bot would find; off while `q` is empty. */
export function useKBSearch(q: string, topic: string | null) {
  const client = useApiClient();
  return useQuery({
    queryKey: queryKeys.kbSearch(q, topic),
    queryFn: () => searchKB(client, q, topic ?? undefined),
    enabled: q.trim() !== "",
  });
}

/** Creates, changes, deletes and imports entries; the base is read again after each. */
export function useKBMutations() {
  const client = useApiClient();
  const queryClient = useQueryClient();
  const onSettled = () => queryClient.invalidateQueries({ queryKey: ["kb"] });
  return {
    create: useMutation({ mutationFn: (body: KBArticleInput) => createKBArticle(client, body), onSettled }),
    update: useMutation({
      mutationFn: ({ id, body }: { id: string; body: KBArticleInput }) => updateKBArticle(client, id, body),
      onSettled,
    }),
    remove: useMutation({ mutationFn: (id: string) => deleteKBArticle(client, id), onSettled }),
    importFile: useMutation({ mutationFn: (body: KBImport) => importKB(client, body), onSettled }),
  };
}

export function useKBGuide() {
  const client = useApiClient();
  return useQuery({ queryKey: queryKeys.kbGuide, queryFn: () => fetchKBGuide(client) });
}

export function useSetKBGuide() {
  const client = useApiClient();
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: KBGuideInput) => setKBGuide(client, body),
    onSuccess: (guide) => queryClient.setQueryData(queryKeys.kbGuide, guide),
  });
}
