import { createApp } from "vue";
import "ant-design-vue/dist/reset.css";
import "./style.css";
import App from "./App.vue";
import LogWindow from "./LogWindow.vue";

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
createApp(view === "logs" ? LogWindow : App).mount("#app");
