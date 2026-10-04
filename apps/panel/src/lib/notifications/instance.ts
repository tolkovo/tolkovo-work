import type { Notifier } from "./notifier";

/**
 * The notifier of this page, created in main.tsx next to the realtime binding. The settings page
 * reaches it for «Отправить тестовое уведомление»; tests install their own.
 */
let current: Notifier | undefined;

export function installNotifier(notifier: Notifier | undefined): void {
  current = notifier;
}

export function currentNotifier(): Notifier | undefined {
  return current;
}
