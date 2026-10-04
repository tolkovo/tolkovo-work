// Applies the saved theme before the first paint so there is no flash (docs/04 §8.2).
// Loaded as an external classic script: the CSP forbids inline scripts (docs/00 §9.6).
// Keep the storage key and values in sync with src/lib/preferences.ts.
(function () {
  try {
    var theme = window.localStorage.getItem("tolkovo.theme");
    if (theme === "light" || theme === "dark") {
      document.documentElement.setAttribute("data-theme", theme);
    }
    var lang = window.localStorage.getItem("tolkovo.lang");
    if (lang === "ru" || lang === "en") {
      document.documentElement.setAttribute("lang", lang);
    }
  } catch (_) {
    // Storage may be unavailable (privacy mode); the system theme applies.
  }
})();
