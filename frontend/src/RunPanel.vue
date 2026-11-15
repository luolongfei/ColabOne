<script setup lang="ts">
import { computed, ref, watch } from "vue";
import {
  PhPlay,
  PhStop,
  PhArrowRight,
  PhGraphicsCard,
} from "@phosphor-icons/vue";
import { command } from "./api";
import type { Snapshot } from "./types";
const props = defineProps<{ state: Snapshot; selected?: string }>();
const emit = defineEmits<{
  models: [];
  api: [];
  chat: [];
  device: [];
  error: [string];
}>();
const chosen = ref(""),
  context = ref(8192),
  temperature = ref(0.7),
  submitting = ref(false),
  notice = ref("");
const models = computed(() =>
  props.state.models.filter(
    (m) => (m.kind || "chat") === "chat" && m.downloaded,
  ),
);
const model = computed(() => models.value.find((m) => m.id === chosen.value));
const ready = computed(() => props.state.runtime.state === "ready");
const task = computed(() =>
  props.state.runtime.tasks?.find((t) => t.state === "running"),
);
const blocked = computed(
  () => submitting.value || !!task.value || !!props.state.operation,
);
watch(
  () => props.selected,
  (value) => {
    if (value) chosen.value = value;
  },
  { immediate: true },
);
watch(
  models,
  () => {
    if (!model.value) chosen.value = models.value[0]?.id || "";
  },
  { immediate: true },
);
function modelDefaults() {
  context.value = model.value?.context_length || 8192;
  temperature.value = model.value?.temperature ?? 0.7;
}
modelDefaults();
async function load() {
  if (!ready.value) return emit("device");
  if (!model.value) return emit("models");
  submitting.value = true;
  notice.value = "";
  try {
    await command("model.load", {
      id: chosen.value,
      context_length: context.value,
      temperature: temperature.value,
    });
    notice.value = "正在远端加载模型，完成后可开始对话。";
  } catch (e) {
    notice.value = String(e);
    emit("error", String(e));
  } finally {
    submitting.value = false;
  }
}
async function unload() {
  try {
    await command("model.unload");
  } catch (e) {
    emit("error", String(e));
  }
}
</script>
<template>
  <main class="page run-page">
    <div class="page-heading">
      <div>
        <h1>运行</h1>
        <p>选择模型，调整参数，然后加载到远端 GPU。</p>
      </div>
      <button class="secondary" @click="emit('models')">管理模型</button>
    </div>
    <div
      class="inline-note"
      style="
        margin-bottom: 24px;
        display: flex;
        align-items: center;
        justify-content: space-between;
        gap: 12px;
        flex-wrap: wrap;
      "
    >
      <span
        >对外 API ·
        {{
          state.runtime.public_api?.state === "ready"
            ? "已开启，其他应用可调用当前模型"
            : "可通过 Cloudflare 将远端模型接入其他应用"
        }}</span
      >
      <button class="text-button" @click="emit('api')">
        管理 API 服务 <PhArrowRight />
      </button>
    </div>
    <div class="run-layout">
      <section class="run-card">
        <h2>模型与设备</h2>
        <label for="run-model">聊天模型</label
        ><select id="run-model" v-model="chosen" @change="modelDefaults">
          <option v-if="!models.length" value="">暂无已下载的聊天模型</option>
          <option v-for="m in models" :key="m.id" :value="m.id">
            {{ m.name }}
          </option>
        </select>
        <p class="field-help">
          {{ model?.description || "先在模型库选择模型，下载到远端设备。" }}
        </p>
        <div class="run-device">
          <PhGraphicsCard :size="32" />
          <div>
            <strong>{{ state.runtime.gpu?.name || "远端 GPU 未连接" }}</strong>
            <p>
              {{
                ready
                  ? "已连接 · 所有模型计算在此设备执行"
                  : "启动设备后可查看可用显存"
              }}
            </p>
          </div>
        </div>
        <dl class="run-stats">
          <div>
            <dt>显存占用</dt>
            <dd>
              {{ ((state.runtime.gpu?.used_mib || 0) / 1024).toFixed(1) }} /
              {{ ((state.runtime.gpu?.total_mib || 0) / 1024).toFixed(0) }} GB
            </dd>
          </div>
          <div>
            <dt>模型文件</dt>
            <dd>
              {{ model ? (model.size / 1024 ** 3).toFixed(2) + " GB" : "—" }}
            </dd>
          </div>
        </dl>
        <p
          v-if="state.runtime.loaded_model && !model?.loaded"
          class="inline-note"
        >
          加载新模型会卸载当前模型，释放显存后再加载。
        </p>
      </section>
      <section class="run-card">
        <h2>生成参数</h2>
        <label>上下文长度</label>
        <div class="option-pills">
          <button
            v-for="value in [8192, 16384, 32768, 65536]"
            :key="value"
            :aria-pressed="context === value"
            @click="context = value"
          >
            {{ value / 1024 }}K
          </button>
        </div>
        <p class="field-help">
          上下文越长，占用的显存越多；也受模型自身支持范围限制。
        </p>
        <label for="run-temperature"
          >回答随机性 <strong>{{ temperature.toFixed(1) }}</strong></label
        ><input
          id="run-temperature"
          v-model.number="temperature"
          type="range"
          min="0"
          max="2"
          step="0.1"
        />
        <div class="range-labels"><span>更稳定</span><span>更有变化</span></div>
        <div v-if="model?.loaded" class="run-loaded">
          <span class="status-dot online" />当前已加载 · 上下文
          {{
            state.runtime.active_options?.context_length ||
            model.context_length ||
            8192
          }}<button class="text-button" @click="emit('chat')">
            开始聊天 <PhArrowRight />
          </button>
        </div>
        <div class="run-actions">
          <button class="primary" :disabled="blocked" @click="load">
            <PhPlay weight="fill" />{{
              !ready
                ? "启动远端 GPU"
                : !model
                  ? "去下载模型"
                  : model.loaded
                    ? "应用参数并重新加载"
                    : "加载模型"
            }}</button
          ><button
            v-if="model?.loaded"
            class="secondary"
            :disabled="blocked"
            @click="unload"
          >
            <PhStop />卸载
          </button>
        </div>
        <p v-if="notice" class="field-help" role="status">{{ notice }}</p>
        <p v-if="task" class="field-help" role="status">{{ task.message }}</p>
      </section>
    </div>
  </main>
</template>
