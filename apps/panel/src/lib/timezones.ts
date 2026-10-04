/** IANA time zones the browser knows, with `current` kept even if the browser lacks it. */
export function timeZoneOptions(current?: string): string[] {
  const zones = Intl.supportedValuesOf("timeZone");
  return current && !zones.includes(current) ? [current, ...zones] : zones;
}

/** The browser's time zone if it is a real IANA name, otherwise undefined (server default). */
export function browserTimeZone(): string | undefined {
  const zone = Intl.DateTimeFormat().resolvedOptions().timeZone;
  return zone && Intl.supportedValuesOf("timeZone").includes(zone) ? zone : undefined;
}
