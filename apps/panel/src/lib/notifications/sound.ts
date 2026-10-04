import type { AlertKind } from "./settings";

/**
 * Short, distinct alert sounds synthesized with Web Audio: no audio files to ship or cache
 * (docs/04 §5.5). Each kind is a few sine notes; "assigned" rises, "message" taps twice, the
 * queue is one soft low note.
 */

type Note = { freq: number; at: number; dur: number };

const PATTERNS: Record<AlertKind, Note[]> = {
  assigned: [
    { freq: 659.25, at: 0, dur: 0.12 },
    { freq: 987.77, at: 0.13, dur: 0.2 },
  ],
  message: [
    { freq: 880, at: 0, dur: 0.07 },
    { freq: 880, at: 0.11, dur: 0.07 },
  ],
  queued: [{ freq: 523.25, at: 0, dur: 0.22 }],
};

export type SoundPlayer = {
  play: (kind: AlertKind, volume: number) => void;
  /** Browsers start audio only after a gesture: call from any click or key press. */
  unlock: () => void;
};

export function createSoundPlayer(): SoundPlayer {
  let ctx: AudioContext | undefined;
  const context = () => {
    if (ctx) return ctx;
    if (typeof AudioContext === "undefined") return undefined;
    ctx = new AudioContext();
    return ctx;
  };
  return {
    unlock() {
      const c = context();
      if (c?.state === "suspended") void c.resume().catch(() => undefined);
    },
    play(kind, volume) {
      const c = context();
      if (!c || volume <= 0) return;
      if (c.state === "suspended") void c.resume().catch(() => undefined);
      const start = c.currentTime + 0.01;
      for (const note of PATTERNS[kind]) {
        const osc = c.createOscillator();
        const gain = c.createGain();
        osc.type = "sine";
        osc.frequency.value = note.freq;
        // A quick attack and decay: no clicks, nothing that lingers.
        const t0 = start + note.at;
        gain.gain.setValueAtTime(0, t0);
        gain.gain.linearRampToValueAtTime(0.3 * volume, t0 + 0.01);
        gain.gain.exponentialRampToValueAtTime(0.0001, t0 + note.dur);
        osc.connect(gain).connect(c.destination);
        osc.start(t0);
        osc.stop(t0 + note.dur + 0.02);
      }
    },
  };
}
