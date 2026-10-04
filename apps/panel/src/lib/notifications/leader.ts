/**
 * One tab of the browser plays sounds and shows desktop notifications, so three open tabs do
 * not ring three times (docs/04 §5.5). The leader holds a Web Lock for as long as it lives; when
 * it closes, the browser hands the lock to the next waiting tab. Without the Web Locks API every
 * tab leads: a duplicate sound is better than a missed one.
 */

export type Leader = {
  isLeader: () => boolean;
  stop: () => void;
};

export const LEADER_LOCK = "tolkovo.notify.leader";

export function createLeader(locks: LockManager | undefined = globalThis.navigator?.locks): Leader {
  if (!locks) return { isLeader: () => true, stop: () => undefined };
  let leading = false;
  let release: (() => void) | undefined;
  const aborter = new AbortController();
  locks
    .request(LEADER_LOCK, { signal: aborter.signal }, () => {
      leading = true;
      return new Promise<void>((resolve) => {
        release = resolve;
      });
    })
    .catch(() => undefined); // aborted while waiting
  return {
    isLeader: () => leading,
    stop() {
      leading = false;
      aborter.abort();
      release?.();
    },
  };
}
