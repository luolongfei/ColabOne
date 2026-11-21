<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from "vue";
import {
  Dropdown as ADropdown,
  Menu as AMenu,
  MenuItem as AMenuItem,
} from "ant-design-vue";
import { PhCaretDown, PhCheck } from "@phosphor-icons/vue";
import ColabOneTooltip from "./ColabOneTooltip.vue";
import { getColabOneFontFamily } from "./colabOneTheme";
import {
  useColabOneI18n,
  type ColabOneLocale,
  type ColabOneLocaleMeta,
} from "./i18n";

withDefaults(
  defineProps<{
    mode?: "topbar" | "settings";
  }>(),
  {
    mode: "topbar",
  },
);

const emit = defineEmits<{
  (e: "changed", meta: ColabOneLocaleMeta): void;
}>();

const { locale, localeMeta, locales, setLocale, t } = useColabOneI18n();
const dropdownOpen = ref(false);

function closeDropdown() {
  dropdownOpen.value = false;
}

function pickLocale(code: ColabOneLocale) {
  dropdownOpen.value = false;
  if (code === locale.value) return;
  setLocale(code, { persist: true, syncNative: true });
  const chosen = locales.find((item) => item.code === code);
  if (chosen) emit("changed", chosen);
}

onMounted(() => {
  window.addEventListener("blur", closeDropdown);
});

onBeforeUnmount(() => {
  window.removeEventListener("blur", closeDropdown);
});
</script>

<template>
  <ADropdown
    v-if="mode === 'topbar'"
    v-model:open="dropdownOpen"
    :trigger="['click']"
    placement="bottomRight"
    overlay-class-name="co-locale-dropdown"
    :destroy-popup-on-hide="true"
  >
    <span class="co-locale-dropdown-anchor">
      <ColabOneTooltip
        :title="t('nav.languageCurrent', { label: localeMeta.nativeLabel })"
        :disabled="dropdownOpen"
        placement="bottom"
      >
        <button
          type="button"
          class="co-locale-trigger"
          :class="{ 'is-open': dropdownOpen }"
          :aria-label="t('nav.languageAria')"
          :aria-expanded="dropdownOpen"
          aria-haspopup="menu"
          data-testid="topbar-locale-trigger"
        >
          <img
            class="co-locale-flag"
            :src="localeMeta.flagUrl"
            :alt="localeMeta.countryCode"
            width="18"
            height="14"
            decoding="async"
          />
          <PhCaretDown :size="12" class="co-locale-caret" aria-hidden="true" />
        </button>
      </ColabOneTooltip>
    </span>

    <template #overlay>
      <AMenu
        class="co-locale-menu"
        :selected-keys="[locale]"
        @click="(info) => pickLocale(String(info.key) as ColabOneLocale)"
      >
        <AMenuItem
          v-for="item in locales"
          :key="item.code"
          class="co-locale-menu-item"
          :data-locale="item.code"
        >
          <span
            class="co-locale-option"
            :lang="item.code"
            :style="{ fontFamily: getColabOneFontFamily(item.code) }"
          >
            <img
              class="co-locale-flag"
              :src="item.flagUrl"
              :alt="item.countryCode"
              width="20"
              height="15"
              decoding="async"
            />
            <span class="co-locale-option-label">{{ item.nativeLabel }}</span>
            <PhCheck
              v-if="locale === item.code"
              :size="14"
              weight="bold"
              class="co-locale-option-check"
              aria-hidden="true"
            />
          </span>
        </AMenuItem>
      </AMenu>
    </template>
  </ADropdown>

  <div
    v-else
    class="co-locale-grid"
    role="radiogroup"
    :aria-label="t('settings.languageLabel')"
    data-testid="settings-locale-grid"
  >
    <button
      v-for="item in locales"
      :key="item.code"
      type="button"
      role="radio"
      class="co-locale-card-btn"
      :class="{ 'is-active': locale === item.code }"
      :aria-checked="locale === item.code"
      :data-locale="item.code"
      :lang="item.code"
      :style="{ fontFamily: getColabOneFontFamily(item.code) }"
      @click="pickLocale(item.code)"
    >
      <img
        class="co-locale-flag"
        :src="item.flagUrl"
        :alt="item.countryCode"
        width="20"
        height="15"
        decoding="async"
      />
      <span class="co-locale-card-label">{{ item.nativeLabel }}</span>
      <PhCheck
        v-if="locale === item.code"
        :size="14"
        weight="bold"
        class="co-locale-option-check"
        aria-hidden="true"
      />
    </button>
  </div>
</template>

<style scoped>
.co-locale-dropdown-anchor {
  display: inline-flex;
  align-items: center;
  flex-shrink: 0;
}

.co-locale-trigger {
  height: 40px;
  padding: 0 11px 0 12px;
  border-radius: 9999px;
  border: 1px solid transparent;
  background: var(--co-track);
  color: var(--co-secondary-text);
  display: inline-flex;
  align-items: center;
  gap: 5px;
  cursor: pointer;
  flex-shrink: 0;
  transition:
    background-color 0.16s ease,
    border-color 0.16s ease,
    color 0.16s ease,
    transform 0.12s ease;
}

:global(:root[data-theme="dark"]) .co-locale-trigger {
  border-color: var(--co-border-secondary);
}

.co-locale-trigger:hover,
.co-locale-trigger.is-open {
  background: var(--co-hover);
  color: var(--co-text);
}

.co-locale-trigger:active {
  transform: scale(0.96);
}

.co-locale-trigger:focus-visible {
  outline: 2px solid var(--co-focus);
  outline-offset: 2px;
}

.co-locale-caret {
  opacity: 0.72;
  flex-shrink: 0;
  transition: transform 0.16s ease;
}

.co-locale-trigger.is-open .co-locale-caret {
  transform: rotate(180deg);
}

.co-locale-flag {
  display: inline-block;
  width: 18px;
  height: 13.5px;
  border-radius: 2.5px;
  object-fit: cover;
  flex-shrink: 0;
  box-shadow: 0 0 0 1px rgba(15, 23, 42, 0.12);
}

:global(:root[data-theme="dark"]) .co-locale-flag {
  box-shadow: 0 0 0 1px rgba(255, 255, 255, 0.18);
}

.co-locale-option {
  display: flex;
  align-items: center;
  gap: 10px;
  min-width: 168px;
  padding: 2px 2px;
}

.co-locale-option .co-locale-flag {
  width: 20px;
  height: 15px;
}

.co-locale-option-label {
  flex: 1 1 auto;
  font-size: 13.5px;
  font-weight: 500;
  white-space: nowrap;
}

.co-locale-option-check {
  color: var(--co-accent);
  flex-shrink: 0;
}

.co-locale-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 8px;
}

@media (max-width: 420px) {
  .co-locale-grid {
    grid-template-columns: 1fr;
  }
}

.co-locale-card-btn {
  display: flex;
  align-items: center;
  gap: 8px;
  min-height: 38px;
  padding: 6px 11px;
  border-radius: 10px;
  border: 1px solid var(--co-border-secondary);
  background: var(--co-subtle);
  color: var(--co-text);
  font-size: 13px;
  font-weight: 500;
  cursor: pointer;
  text-align: left;
  transition:
    background 0.15s ease,
    border-color 0.15s ease,
    box-shadow 0.15s ease;
}

.co-locale-card-btn:hover {
  background: var(--co-soft-hover);
  border-color: var(--co-border);
}

.co-locale-card-btn.is-active {
  background: var(--co-selected);
  border-color: var(--co-accent);
  color: var(--co-accent-strong);
  font-weight: 600;
}

.co-locale-card-btn:focus-visible {
  outline: 2px solid var(--co-focus);
  outline-offset: 2px;
}

.co-locale-card-btn .co-locale-flag {
  width: 20px;
  height: 15px;
}

.co-locale-card-label {
  flex: 1 1 auto;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
</style>
