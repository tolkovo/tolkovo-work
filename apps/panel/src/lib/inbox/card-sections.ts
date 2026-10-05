import { z } from "zod";
import { create } from "zustand";

/**
 * Which sections of the customer card are folded (docs/07 §4: "секции сворачиваются"). A personal
 * convenience of this browser, so localStorage is enough; technical details start folded.
 */

export const CARD_SECTIONS = ["who", "issues", "history", "tech"] as const;
export type CardSection = (typeof CARD_SECTIONS)[number];

export const CARD_SECTIONS_KEY = "tolkovo.card.collapsed";
const DEFAULT_COLLAPSED: CardSection[] = ["tech"];

/** Stored value, or the default when nothing valid is stored (localStorage is external input). */
export function readCollapsed(): CardSection[] {
  try {
    const raw = window.localStorage.getItem(CARD_SECTIONS_KEY);
    if (raw === null) return DEFAULT_COLLAPSED;
    return z.array(z.enum(CARD_SECTIONS)).catch(DEFAULT_COLLAPSED).parse(JSON.parse(raw));
  } catch {
    return DEFAULT_COLLAPSED;
  }
}

function writeCollapsed(collapsed: CardSection[]): void {
  try {
    window.localStorage.setItem(CARD_SECTIONS_KEY, JSON.stringify(collapsed));
  } catch {
    // Storage unavailable (privacy mode, quota): the choice lasts for this session only.
  }
}

type CardSectionsState = {
  collapsed: CardSection[];
  toggle: (section: CardSection) => void;
};

export const useCardSections = create<CardSectionsState>((set, get) => ({
  collapsed: readCollapsed(),
  toggle: (section) => {
    const { collapsed } = get();
    const next = collapsed.includes(section) ? collapsed.filter((s) => s !== section) : [...collapsed, section];
    writeCollapsed(next);
    set({ collapsed: next });
  },
}));
