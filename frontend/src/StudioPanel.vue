<script setup lang="ts">
import { computed, nextTick, onUnmounted, ref, watch } from "vue";
import {
  PhImage,
  PhVideoCamera,
  PhDownloadSimple,
  PhCopy,
  PhArrowCounterClockwise,
  PhTrash,
  PhX,
  PhMagnifyingGlassPlus,
  PhUploadSimple,
} from "@phosphor-icons/vue";
import { command, exportCreation } from "./api";
import type { Snapshot } from "./types";
import {
  creationURL,
  studioDrafts,
  type Creation,
  type CreativeParams,
} from "./creative";
const props = defineProps<{ state: Snapshot; kind: "image" | "video" }>();
const emit = defineEmits<{ device: []; models: []; error: [string] }>();
const ready = computed(() => props.state.runtime.state === "ready");
const models = computed(() =>
  props.state.models.filter((m) => m.kind === props.kind),
);
const modelID = ref("");
const model = computed(() => models.value.find((m) => m.id === modelID.value));
const task = computed(() =>
  props.state.runtime.tasks?.find((t) => t.state === "running"),
);
const sending = ref(false),
  notice = ref(""),
  view = ref("form"),
  focused = ref(""),
  zoom = ref(100),
  removing = ref<Creation | null>(null);
const form = ref<CreativeParams>({
  model_id: "",
  kind: props.kind,
  prompt: "",
  negative_prompt: "",
  width: 1024,
  height: 1024,
  steps: 9,
  count: 1,
  seed: -1,
  strength: 0.65,
  reference_id: "",
  frames: 33,
  fps: 16,
});
const ratio = ref("1:1"),
  resolution = ref("1024"),
  seconds = ref(2),
  referenceName = ref("");
const references = ref<HTMLInputElement | null>(null);
const items = computed(() =>
  (props.state.creations || []).filter((item) => item.kind === props.kind),
);
const urls = ref<Record<string, string>>({}),
  fetching = ref<Record<string, boolean>>({}),
  failed = ref<Record<string, string>>({});
let disposed = false;
const blocked = computed(
  () => sending.value || !!task.value || !!props.state.operation,
);
const isVideo = computed(() => props.kind === "video");
const capability = computed(() => (props.state.runtime.protocol || 0) >= 3);
watch(
  [models, () => props.kind],
  () => {
    if (!models.value.some((m) => m.id === modelID.value))
      modelID.value = models.value[0]?.id || "";
  },
  { immediate: true },
);
watch(
  () => props.kind,
  (kind) => {
    form.value.kind = kind;
    form.value.steps = kind === "video" ? 20 : 9;
    form.value.count = 1;
    form.value.reference_id = "";
    referenceName.value = "";
    ratio.value = kind === "video" ? "16:9" : "1:1";
    resolution.value = kind === "video" ? "480" : "1024";
    focused.value = "";
    notice.value = "";
  },
  { immediate: true },
);
function dimensions() {
  if (isVideo.value) {
    const landscape = resolution.value === "720" ? [1280, 736] : [832, 480];
    [form.value.width, form.value.height] = (
      ratio.value === "9:16" ? landscape.reverse() : landscape
    ) as [number, number];
  } else if (resolution.value !== "custom") {
    const [w, h] = ratio.value.split(":").map(Number) as [number, number];
    const edge = Number(resolution.value);
    form.value.width = Math.round((edge * w) / Math.max(w, h) / 32) * 32;
    form.value.height = Math.round((edge * h) / Math.max(w, h) / 32) * 32;
  }
}
watch([ratio, resolution], dimensions, { immediate: true });
const savedDraft = studioDrafts[props.kind];
if (savedDraft) {
  ratio.value = savedDraft.ratio;
  resolution.value = savedDraft.resolution;
  seconds.value = savedDraft.seconds;
  Object.assign(form.value, savedDraft.params);
  if (savedDraft.runtimeId === props.state.runtime.id)
    referenceName.value = savedDraft.referenceName;
  else form.value.reference_id = "";
}

const buttonText = computed(() =>
  !ready.value
    ? "先启动远端 GPU"
    : !capability.value
      ? "需要更新远端服务"
      : !model.value?.downloaded
        ? "下载模型到远端"
        : !model.value?.loaded
          ? "加载到远端 GPU"
          : `生成${isVideo.value ? "视频" : "图片"}`,
);
async function action() {
  if (!ready.value) return emit("device");
  if (!model.value || blocked.value || !capability.value) return;
  sending.value = true;
  notice.value = "";
  try {
    if (!model.value.downloaded) {
      await command("model.download", { id: modelID.value });
      notice.value = "正在下载模型和配套组件，完成后即可加载。";
    } else if (!model.value.loaded) {
      await command("model.load", { id: modelID.value });
      notice.value = "正在准备远端引擎并加载模型，首次准备可能需要数分钟。";
    } else {
      form.value.model_id = modelID.value;
      if (isVideo.value) form.value.frames = seconds.value * form.value.fps + 1;
      await command("creative.generate", { ...form.value });
      notice.value = "已提交到远端。生成期间不会触发闲置释放。";
    }
  } catch (e) {
    notice.value = String(e);
    emit("error", String(e));
  } finally {
    sending.value = false;
  }
}
async function reference(event: Event) {
  const input = event.target as HTMLInputElement,
    file = input.files?.[0];
  input.value = "";
  if (!file) return;
  if (
    file.size > 8 * 1024 ** 2 ||
    !["image/png", "image/jpeg", "image/webp"].includes(file.type)
  ) {
    notice.value = "请选择不超过 8 MB 的 PNG、JPEG 或 WebP 图片";
    return;
  }
  sending.value = true;
  try {
    const data = await new Promise<string>((resolve, reject) => {
      const reader = new FileReader();
      reader.onload = () => resolve(String(reader.result));
      reader.onerror = reject;
      reader.readAsDataURL(file);
    });
    const result = await command("creative.reference", { data });
    form.value.reference_id = result.id;
    referenceName.value = file.name;
    notice.value = "参考图已传到当前远端 GPU。";
  } catch (e) {
    notice.value = String(e);
  } finally {
    sending.value = false;
  }
}
async function preview(item: Creation) {
  if (urls.value[item.id] || fetching.value[item.id] || !ready.value) return;
  fetching.value[item.id] = true;
  delete failed.value[item.id];
  try {
    const url = await creationURL(item);
    if (disposed) URL.revokeObjectURL(url);
    else urls.value[item.id] = url;
  } catch (e) {
    failed.value[item.id] = String(e);
  } finally {
    fetching.value[item.id] = false;
  }
}
watch(
  [items, ready],
  () => {
    // Limit automatic previews; large video files transfer only on demand.
    for (const item of items.value.slice(0, 4))
      if (
        item.kind === "image" &&
        !failed.value[item.id] &&
        !Object.values(fetching.value).some(Boolean)
      )
        void preview(item);
  },
  { immediate: true },
);
async function reuse(item: Creation) {
  modelID.value = item.params.model_id;
  Object.assign(form.value, item.params, { reference_id: "" });
  ratio.value = item.params.width >= item.params.height ? "16:9" : "9:16";
  resolution.value = isVideo.value
    ? Math.max(item.params.width, item.params.height) > 832
      ? "720"
      : "480"
    : "custom";
  await nextTick();
  Object.assign(form.value, item.params, { reference_id: "" });
  referenceName.value = "";
  view.value = "form";
  seconds.value = Math.round((item.params.frames - 1) / item.params.fps);
  notice.value = "已复用提示词、尺寸和种子。参考图需要重新选择。";
}
async function download(item: Creation) {
  try {
    const path = await exportCreation(item.id);
    if (path) notice.value = "作品已导出到你选择的位置。";
  } catch (e) {
    notice.value = String(e);
  }
}
async function copyPrompt(item: Creation) {
  try {
    await navigator.clipboard.writeText(item.params.prompt);
    notice.value = "提示词已复制";
  } catch {
    notice.value = "无法写入剪贴板，请选择提示词后复制";
  }
}
async function remove() {
  if (!removing.value) return;
  try {
    await command("creative.delete", { id: removing.value.id });
    removing.value = null;
    notice.value = "作品已移到远端及云盘的回收目录";
  } catch (e) {
    notice.value = String(e);
  }
}
watch(
  () => props.state.runtime.id,
  () => {
    form.value.reference_id = "";
    referenceName.value = "";
    failed.value = {};
  },
);
onUnmounted(() => {
  studioDrafts[props.kind] = {
    params: { ...form.value },
    ratio: ratio.value,
    resolution: resolution.value,
    seconds: seconds.value,
    referenceName: referenceName.value,
    runtimeId: props.state.runtime.id,
  };
  disposed = true;
  Object.values(urls.value).forEach(URL.revokeObjectURL);
});
</script>
<template>
  <main class="page studio-page">
    <div class="page-heading">
      <div class="studio-title">
        <h1>{{ isVideo ? "视频" : "画图" }}</h1>
        <div class="segmented" role="tablist" aria-label="创作视图">
          <button
            role="tab"
            :aria-selected="view === 'form'"
            @click="view = 'form'"
          >
            表单</button
          ><button
            role="tab"
            :aria-selected="view === 'canvas'"
            @click="view = 'canvas'"
          >
            画布
          </button>
        </div>
      </div>
      <div class="form-row">
        <button
          v-if="model?.loaded"
          class="secondary"
          :disabled="blocked"
          @click="command('model.unload').catch((e) => (notice = String(e)))"
        >
          停止创作引擎</button
        ><button class="secondary" @click="emit('models')">管理创作模型</button>
      </div>
    </div>
    <p class="studio-intro">
      {{
        isVideo
          ? "写下动作与镜头，让画面在远端动起来。"
          : "描述你想要的画面，由远端 GPU 完成生成。"
      }}
      作品可预览、复用和导出。
    </p>
    <div v-if="notice" class="studio-notice" role="status">
      {{ notice
      }}<button
        class="icon-button"
        aria-label="关闭创作提示"
        @click="notice = ''"
      >
        <PhX />
      </button>
    </div>
    <div class="studio-workspace" :class="{ 'canvas-view': view === 'canvas' }">
      <form
        v-show="view === 'form'"
        class="studio-form"
        @submit.prevent="action"
      >
        <label
          >模型<select v-model="modelID" aria-label="创作模型">
            <option v-for="m in models" :key="m.id" :value="m.id">
              {{ m.name }} ·
              {{ m.loaded ? "已加载" : m.downloaded ? "已下载" : "未下载" }}
            </option>
          </select></label
        >
        <p class="field-help">
          {{ model?.description
          }}<template v-if="model">
            ·
            {{ (model.size / 1024 ** 3).toFixed(2) }} GB（含全部组件）</template
          >
        </p>
        <label
          >描述你想要的{{ isVideo ? "视频" : "画面"
          }}<textarea
            v-model="form.prompt"
            rows="6"
            maxlength="12000"
            aria-label="创作提示词"
            :placeholder="
              isVideo
                ? '主体、动作、镜头移动和氛围…'
                : '例如：雨后的蓝色海边小屋，柔和晨光，水彩插画…'
            "
          />
        </label>
        <p class="field-help">可以直接写中文，描述主体、场景、风格和光线。</p>
        <details v-if="isVideo">
          <summary>不想出现的内容（反向提示词）</summary>
          <textarea
            v-model="form.negative_prompt"
            rows="3"
            aria-label="反向提示词"
          />
        </details>
        <fieldset>
          <legend>画面比例和清晰度</legend>
          <div class="choice-row">
            <button
              v-for="r in isVideo
                ? ['16:9', '9:16']
                : ['1:1', '3:4', '4:3', '9:16', '16:9']"
              :key="r"
              type="button"
              :class="{ active: ratio === r }"
              :aria-pressed="ratio === r"
              @click="ratio = r"
            >
              {{ r }}
            </button>
          </div>
          <div class="choice-row">
            <button
              v-for="r in isVideo
                ? ['480', '720']
                : ['1024', '2048', '4096', 'custom']"
              :key="r"
              type="button"
              :class="{ active: resolution === r }"
              :aria-pressed="resolution === r"
              @click="resolution = r"
            >
              {{
                r === "custom"
                  ? "自定义"
                  : isVideo
                    ? r + "P"
                    : Number(r) / 1024 + "K"
              }}
            </button>
          </div>
          <div v-if="resolution === 'custom' && !isVideo" class="form-grid">
            <label
              >宽<input
                v-model.number="form.width"
                type="number"
                min="256"
                max="4096"
                step="32" /></label
            ><label
              >高<input
                v-model.number="form.height"
                type="number"
                min="256"
                max="4096"
                step="32"
            /></label>
          </div>
          <p class="field-help">
            实际尺寸 {{ form.width }} × {{ form.height
            }}<span v-if="Number(resolution) >= 2048">
              · 高清生成需要更多显存和时间</span
            >
          </p>
        </fieldset>
        <div class="form-grid three">
          <label
            >步数<input
              v-model.number="form.steps"
              type="number"
              min="1"
              max="80"
              aria-label="生成步数" /></label
          ><label v-if="!isVideo"
            >张数<select v-model.number="form.count" aria-label="生成张数">
              <option v-for="n in [1, 2, 3, 4]" :key="n" :value="n">
                {{ n }}
              </option>
            </select></label
          ><label v-else
            >时长<select v-model.number="seconds" aria-label="视频时长">
              <option v-for="n in [2, 3, 5]" :key="n" :value="n">
                {{ n }} 秒
              </option>
            </select></label
          ><label
            >种子<input
              v-model.number="form.seed"
              type="number"
              min="-1"
              max="2147483647"
              aria-label="生成种子"
          /></label>
        </div>
        <p class="field-help">
          种子 -1 为随机；复用作品参数可保留实际种子。{{
            isVideo ? "视频以 16 FPS 生成。" : ""
          }}
        </p>
        <label>{{ isVideo ? "首帧图片" : "参考图片" }}（可选）</label
        ><input
          ref="references"
          class="visually-hidden"
          type="file"
          accept="image/png,image/jpeg,image/webp"
          aria-label="选择参考图片"
          @change="reference"
        />
        <button
          type="button"
          class="reference-picker"
          :disabled="!ready || blocked || !capability"
          @click="references?.click()"
        >
          <PhUploadSimple :size="18" />{{
            referenceName ||
            (isVideo
              ? "上传首帧，让画面继续运动"
              : "上传一张图片，在它的基础上重绘")
          }}
        </button>
        <div v-if="form.reference_id" class="reference-options">
          <button
            type="button"
            class="text-button"
            @click="
              form.reference_id = '';
              referenceName = '';
            "
          >
            移除参考图</button
          ><label v-if="!isVideo"
            >重绘强度
            <input
              v-model.number="form.strength"
              type="range"
              min="0.05"
              max="1"
              step="0.05"
            />{{ form.strength }}</label
          >
        </div>
        <p
          v-if="state.runtime.loaded_model && !model?.loaded"
          class="field-help"
        >
          加载此模型会卸载当前模型，已下载文件保留。
        </p>
        <p v-if="ready && !capability" class="inline-note">
          请先卸载当前模型，更新远端管理服务后使用创作功能。
        </p>
        <button
          class="primary studio-generate"
          :disabled="
            blocked ||
            (ready &&
              (!capability || !model || (model.loaded && !form.prompt.trim())))
          "
        >
          <component :is="isVideo ? PhVideoCamera : PhImage" :size="20" />{{
            buttonText
          }}
        </button>
        <div v-if="task" class="studio-task" role="status">
          <strong>{{ task.message || "远端任务进行中" }}</strong><progress
            :value="task.progress"
            max="1"
          /><button
            type="button"
            class="text-button danger-text"
            @click="
              command('task.cancel', { id: task.id }).catch(
                (e) => (notice = String(e)),
              )
            "
          >
            停止当前任务
          </button>
        </div>
      </form>
      <section class="studio-gallery" aria-label="生成作品">
        <div v-if="view === 'canvas'" class="canvas-toolbar">
          <span>作品画布</span
          ><label
            ><PhMagnifyingGlassPlus />缩放
            <input
              v-model.number="zoom"
              type="range"
              min="50"
              max="200"
              step="10"
            />{{ zoom }}%</label
          >
        </div>
        <div v-if="!items.length" class="studio-empty">
          <component
            :is="isVideo ? PhVideoCamera : PhImage"
            :size="44"
            weight="light"
          />
          <h2>
            {{
              isVideo
                ? "第一段视频，从一个画面开始"
                : "你的下一张作品，从一句话开始"
            }}
          </h2>
          <p>选择模型，写下描述。生成结果会出现在这里。</p>
          <small>{{
            state.setup.drive
              ? "已连接 Google Drive，完成后自动备份。"
              : "连接 Google Drive 可保留作品；释放 GPU 前也可以导出。"
          }}</small>
        </div>
        <div
          v-else
          class="creation-grid"
          :class="{ 'creation-canvas': view === 'canvas' }"
        >
          <article
            v-for="item in items"
            :key="item.id"
            class="creation-card"
            :class="{ focused: focused === item.id }"
          >
            <div
              class="creation-preview"
              :style="view === 'canvas' ? { zoom: zoom / 100 } : {}"
              @click="focused = item.id"
            >
              <img
                v-if="item.kind === 'image' && urls[item.id]"
                :src="urls[item.id]"
                :alt="item.params.prompt"
              />
              <video
                v-else-if="urls[item.id]"
                :src="urls[item.id]"
                controls
                preload="metadata"
              />
              <button
                v-else
                class="preview-load"
                :disabled="!ready || fetching[item.id]"
                @click="preview(item)"
              >
                <component
                  :is="item.kind === 'image' ? PhImage : PhVideoCamera"
                  :size="28"
                />{{
                  fetching[item.id]
                    ? "正在读取远端作品…"
                    : failed[item.id]
                      ? "重试预览"
                      : "预览作品"
                }}
              </button>
            </div>
            <div class="creation-caption">
              <p>{{ item.params.prompt }}</p>
              <small
                >{{ item.model }} · {{ item.params.width }}×{{
                  item.params.height
                }}
                · {{ item.elapsed.toFixed(1) }} 秒</small
              ><small
                >种子 {{ item.params.seed }} ·
                {{ item.synced ? "已备份云盘" : "保存在远端 GPU" }}</small
              >
              <p v-if="failed[item.id]" class="danger-text">
                {{ failed[item.id] }}
              </p>
              <div class="creation-actions">
                <button @click="reuse(item)">
                  <PhArrowCounterClockwise />复用</button
                ><button @click="copyPrompt(item)"><PhCopy />提示词</button
                ><button :disabled="!ready" @click="download(item)">
                  <PhDownloadSimple />导出</button
                ><button
                  :disabled="!ready"
                  @click="removing = item"
                  aria-label="删除作品"
                >
                  <PhTrash />
                </button>
              </div>
            </div>
          </article>
        </div>
      </section>
    </div>
    <div v-if="removing" class="modal-backdrop" @click.self="removing = null">
      <section
        class="modal"
        role="dialog"
        aria-modal="true"
        aria-label="删除作品"
      >
        <h2>移除这件作品？</h2>
        <p>作品将移到远端和云盘的回收目录；已导出的文件保留。</p>
        <div class="form-row">
          <button class="secondary" @click="removing = null">取消</button
          ><button class="danger" @click="remove">移到回收目录</button>
        </div>
      </section>
    </div>
  </main>
</template>
