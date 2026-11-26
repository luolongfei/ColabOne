import { createApp } from "vue";
import "ant-design-vue/dist/reset.css";
import "./style.css";
import "./mobile.css";
import { installColabOneWindowFocusGuard } from "./colabOneFocusGuard";

const windowFocusGuard = installColabOneWindowFocusGuard();

// ColabOne auto-hiding 4px capsule scrollbar activator.
const scrollTimers = new WeakMap<Element, ReturnType<typeof setTimeout>>();
window.addEventListener(
  "scroll",
  (event) => {
    const target = event.target;
    const el =
      target instanceof Element
        ? target
        : document.scrollingElement || document.documentElement;
    if (!el) return;
    el.setAttribute("data-scrolling", "true");
    const prev = scrollTimers.get(el);
    if (prev) clearTimeout(prev);
    scrollTimers.set(
      el,
      setTimeout(() => {
        el.removeAttribute("data-scrolling");
        scrollTimers.delete(el);
      }, 900),
    );
  },
  { capture: true, passive: true },
);

const view = new URLSearchParams(window.location.search).get("view");
const loadRoot =
  view === "logs"
    ? () => import("./LogWindow.vue")
    : () => import("./App.vue");

function colabOneMountRecoveryNotice(err: unknown) {
  const host = document.getElementById("app");
  if (!host) return;

  const lang = (
    document.documentElement.lang ||
    navigator.language ||
    ""
  ).toLowerCase();
  const isZh = lang.startsWith("zh");

  const wrap = document.createElement("div");
  wrap.setAttribute("role", "alert");
  wrap.style.height = "100%";
  wrap.style.display = "flex";
  wrap.style.alignItems = "center";
  wrap.style.justifyContent = "center";
  wrap.style.padding = "24px";
  wrap.style.boxSizing = "border-box";
  wrap.style.background = "var(--co-canvas, #fafbfc)";
  wrap.style.color = "var(--co-text, #111827)";

  const card = document.createElement("div");
  card.style.maxWidth = "420px";
  card.style.width = "100%";
  card.style.padding = "24px";
  card.style.borderRadius = "var(--co-radius-md, 12px)";
  card.style.background = "var(--co-surface, #ffffff)";
  card.style.border = "1px solid var(--co-border, #e5e7eb)";
  card.style.boxShadow =
    "var(--co-card-shadow, 0 1px 3px rgba(15, 23, 42, 0.08))";
  card.style.textAlign = "center";

  const title = document.createElement("h1");
  title.textContent = isZh ? "界面加载失败" : "Failed to load interface";
  title.style.margin = "0 0 8px";
  title.style.fontSize = "16px";
  title.style.fontWeight = "600";

  const desc = document.createElement("p");
  desc.textContent = isZh
    ? "应用资源未能完成加载，请点击下方按钮重新加载。"
    : "The application view could not be loaded. Click below to reload.";
  desc.style.margin = "0 0 16px";
  desc.style.fontSize = "13px";
  desc.style.color = "var(--co-secondary-text, #4b5563)";

  const detailText =
    err instanceof Error ? err.message : typeof err === "string" ? err : "";
  if (detailText.trim()) {
    const detail = document.createElement("p");
    detail.textContent = detailText.trim().slice(0, 240);
    detail.style.margin = "0 0 16px";
    detail.style.fontSize = "12px";
    detail.style.fontFamily = "var(--co-font-mono, monospace)";
    detail.style.color = "var(--co-muted, #6b7280)";
    detail.style.wordBreak = "break-word";
    card.appendChild(title);
    card.appendChild(desc);
    card.appendChild(detail);
  } else {
    card.appendChild(title);
    card.appendChild(desc);
  }

  const button = document.createElement("button");
  button.type = "button";
  button.textContent = isZh ? "重新加载" : "Reload";
  button.style.padding = "8px 18px";
  button.style.border = "none";
  button.style.borderRadius = "var(--co-radius, 10px)";
  button.style.background = "var(--co-accent, #3b82f6)";
  button.style.color = "var(--co-on-accent, #ffffff)";
  button.style.fontSize = "13px";
  button.style.fontWeight = "500";
  button.style.cursor = "pointer";
  button.addEventListener("click", () => {
    window.location.reload();
  });

  card.appendChild(button);
  wrap.appendChild(card);
  host.replaceChildren(wrap);
}

async function colabOneLoadRootWithRetry() {
  try {
    return await loadRoot();
  } catch {
    await new Promise<void>((resolve) => setTimeout(resolve, 250));
    return await loadRoot();
  }
}

void colabOneLoadRootWithRetry()
  .then(({ default: Root }) => {
    createApp(Root).mount("#app");
    windowFocusGuard.onMounted();
  })
  .catch((err) => {
    console.error("[ColabOne] Failed to bootstrap root view:", err);
    colabOneMountRecoveryNotice(err);
  });
