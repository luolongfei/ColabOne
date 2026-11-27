// Apply theme & locale before styles and Vue to avoid flash on launch.
(() => {
  let choice = "system";
  let storedLocale = "";
  try {
    choice = localStorage.getItem("colabone.appearance") || "system";
    const isShareRoute =
      location.pathname === "/share" ||
      location.pathname.startsWith("/share/") ||
      location.pathname === "/gallery" ||
      location.pathname.startsWith("/gallery/");
    storedLocale =
      localStorage.getItem(
        isShareRoute ? "colabone.share.locale" : "colabone.locale",
      ) || "";
  } catch {}
  const dark =
    choice === "dark" ||
    (choice !== "light" &&
      matchMedia("(prefers-color-scheme: dark)").matches);
  document.documentElement.dataset.theme = dark ? "dark" : "light";
  document.documentElement.style.colorScheme = dark ? "dark" : "light";

  const matchLocale = (raw) => {
    const s = String(raw || "").trim().replace(/_/g, "-").toLowerCase();
    if (!s) return "";
    if (
      s === "zh-hk" ||
      s === "zh-mo" ||
      s.startsWith("zh-hant-hk") ||
      s.startsWith("zh-hant-mo") ||
      s === "yue" ||
      s.startsWith("yue-")
    ) {
      return "zh-HK";
    }
    if (s === "zh-tw" || s.startsWith("zh-hant")) return "zh-TW";
    if (s === "zh" || s === "zh-cn" || s === "zh-sg" || s.startsWith("zh-hans")) {
      return "zh-CN";
    }
    if (s === "ja" || s.startsWith("ja-")) return "ja-JP";
    if (s === "ko" || s.startsWith("ko-")) return "ko-KR";
    if (s === "en" || s.startsWith("en-")) return "en-US";
    return "";
  };
  let lang = matchLocale(storedLocale);
  if (!lang) {
    const list =
      Array.isArray(navigator.languages) && navigator.languages.length
        ? navigator.languages
        : [navigator.language];
    for (const item of list) {
      const m = matchLocale(item);
      if (m) {
        lang = m;
        break;
      }
    }
  }
  document.documentElement.lang = lang || "en-US";
})();
