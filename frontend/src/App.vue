<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from "vue";
import {
  PhCirclesThree,
  PhHardDrives,
  PhCube,
  PhChatCircle,
  PhGearSix,
  PhArrowUp,
  PhArrowSquareOut,
  PhCheck,
  PhDownloadSimple,
  PhPlay,
  PhStop,
  PhTrash,
  PhArrowsClockwise,
  PhPlus,
  PhWarningCircle,
  PhX,
  PhCopy,
  PhCloud,
  PhCaretRight,
} from "@phosphor-icons/vue";
import { marked } from "marked";
import DOMPurify from "dompurify";
import {
  snapshot,
  command,
  openURL,
  exportConversation,
  readClipboard,
} from "./api";
import { initialSnapshot, labels, canLoad, type ModelSpec } from "./types";
const state = ref(initialSnapshot()),
  page = ref("device"),
  error = ref(""),
  toast = ref(""),
  busy = ref(false);
const nav = [
  { id: "device", label: "远端设备", icon: PhHardDrives },
  { id: "models", label: "模型库", icon: PhCube },
  { id: "chat", label: "聊天", icon: PhChatCircle },
  { id: "settings", label: "设置", icon: PhGearSix },
];
const ready = computed(() => state.value.runtime.state === "ready");
const task = computed(() =>
  state.value.runtime.tasks?.find((t) => t.state === "running"),
);
const selected = ref(""),
  draft = ref(""),
  search = ref(""),
  showImport = ref(false),
  showRelease = ref(false),
  force = ref(false),
  deleteTarget = ref<ModelSpec | null>(null);
const conversation = computed(() =>
  state.value.conversations.find((c) => c.id === selected.value),
);
const messages = computed(() => {
  const list = conversation.value?.messages.map((m) => ({ ...m })) || [];
  const stream = state.value.stream;
  if (
    stream &&
    stream.conversation_id === selected.value &&
    stream.state === "running"
  ) {
    if (!list.length || list[list.length - 1]?.role === "user") {
      if (!list.length)
        list.push({ role: "user", content: stream.text, status: "complete" });
      list.push({
        role: "assistant",
        content: stream.content,
        status: "streaming",
      });
    } else {
      list[list.length - 1] = {
        role: "assistant",
        content: stream.content,
        status: "streaming",
      };
    }
  }
  return list;
});
const filtered = computed(() =>
  state.value.models.filter((m) =>
    (m.name + " " + m.description)
      .toLowerCase()
      .includes(search.value.toLowerCase()),
  ),
);
const loaded = computed(() =>
  state.value.models.find((m) => m.id === state.value.runtime.loaded_model),
);
const form = ref({
  gpu: "T4",
  max_cu: 5,
  idle_minutes: 30,
  auto_drive: true,
  hf_token: "",
});
const code = ref(""),
  showAuthPaste = ref(false),
  importForm = ref({ repo: "", filename: "", revision: "main" });
let timer: ReturnType<typeof setTimeout>,
  toastTimer: ReturnType<typeof setTimeout>,
  stopped = false;
let initialized = false,
  openedAuth = "";
const bytes = (n = 0) =>
  n >= 1024 ** 3
    ? (n / 1024 ** 3).toFixed(1) + " GB"
    : (n / 1024 ** 2).toFixed(0) + " MB";
const money = (n?: number) => (n === undefined ? "未读取" : n.toFixed(2));
const html = (text: string) =>
  DOMPurify.sanitize(marked.parse(text, { async: false }) as string).replaceAll(
    "<pre>",
    '<pre><button type="button" data-copy-code="true" class="copy-code">复制代码</button>',
  );
function codeClick(event: MouseEvent) {
  const button = (event.target as Element).closest("[data-copy-code]");
  const text = button?.closest("pre")?.querySelector("code")?.textContent;
  if (text) copy(text);
}
function notify(text: string) {
  toast.value = text;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => (toast.value = ""), 3500);
}
async function refresh() {
  try {
    state.value = await snapshot();
    if (!initialized) {
      Object.assign(form.value, state.value.settings);
      initialized = true;
    }
  } catch (e) {
    error.value = String(e);
  }
  if (!stopped) timer = setTimeout(refresh, 600);
}
async function act(action: string, payload: Record<string, unknown> = {}) {
  busy.value = true;
  error.value = "";
  try {
    return await command(action, payload);
  } catch (e) {
    error.value = String(e);
    return null;
  } finally {
    busy.value = false;
  }
}
async function beginLogin() {
  await act("auth.colab.auto");
}
async function manualLogin() {
  const result = await act("auth.colab.begin");
  if (result) {
    showAuthPaste.value = true;
    openedAuth = result.url;
    await openURL(result.url);
  }
}
async function finishLogin() {
  const reply = code.value.trim();
  if (!reply) return;
  if (await act("auth.colab.finish", { code: reply })) {
    code.value = "";
    notify("正在验证授权，请稍候");
  }
}
async function pasteLogin() {
  try {
    code.value = await readClipboard();
    await finishLogin();
  } catch {
    error.value = "未能读取剪贴板，请直接粘贴到下方输入框";
  }
}
watch(
  () => state.value.auth_url,
  async (url) => {
    if (url && url !== openedAuth) {
      openedAuth = url;
      try {
        await openURL(url);
      } catch (e) {
        error.value = String(e);
      }
    }
  },
);
async function save() {
  if (await act("settings.save", form.value)) {
    form.value.hf_token = "";
    notify("设置已保存");
  }
}
async function start() {
  if (
    await act("settings.save", {
      gpu: form.value.gpu,
      max_cu: form.value.max_cu,
      idle_minutes: form.value.idle_minutes,
      auto_drive: form.value.auto_drive,
    })
  )
    await act("runtime.start");
}
async function release() {
  const result = await act("runtime.release", { force: force.value });
  if (result) showRelease.value = false;
}
async function send() {
  const text = draft.value.trim();
  if (!text || !ready.value || !loaded.value || task.value) return;
  const result = await act("chat.send", {
    conversation_id: selected.value,
    text,
  });
  if (result) {
    selected.value = result.conversation_id;
    draft.value = "";
  }
}
async function importModel() {
  if (await act("model.import", importForm.value)) {
    showImport.value = false;
    importForm.value = { repo: "", filename: "", revision: "main" };
  }
}
async function copy(text: string) {
  await navigator.clipboard.writeText(text);
  notify("已复制");
}
async function exportChat() {
  if (conversation.value)
    await exportConversation(
      "# " +
        conversation.value.title +
        "\n\n" +
        conversation.value.messages
          .map(
            (m) =>
              "## " + (m.role === "user" ? "你" : "助手") + "\n\n" + m.content,
          )
          .join("\n\n"),
    );
}
function inputKey(e: KeyboardEvent) {
  if (e.key === "Enter" && !e.shiftKey && !e.isComposing) {
    e.preventDefault();
    send();
  }
}
watch(
  () => messages.value.map((m) => m.content).join(""),
  () =>
    requestAnimationFrame(() => {
      const pane = document.querySelector(".messages");
      if (pane) pane.scrollTop = pane.scrollHeight;
    }),
);
onMounted(refresh);
onUnmounted(() => {
  stopped = true;
  clearTimeout(timer);
  clearTimeout(toastTimer);
});
</script>

<template>
  <div class="app-shell">
    <aside class="sidebar">
      <div class="brand">
        <PhCirclesThree :size="28" weight="duotone" /><span>ColabOne</span>
      </div>
      <p class="sidebar-label">工作空间</p>
      <nav aria-label="主导航">
        <button
          v-for="item in nav"
          :key="item.id"
          :class="{ active: page === item.id }"
          @click="page = item.id"
        >
          <component :is="item.icon" :size="20" /><span>{{ item.label }}</span>
        </button>
      </nav>
      <div class="sidebar-bottom">
        <PhCloud :size="20" />
        <div>
          <strong>远端运行</strong><small>模型与推理留在 GPU 设备</small>
        </div>
      </div>
    </aside>
    <div class="workspace">
      <header class="topbar">
        <div class="connection">
          <span :class="['status-dot', ready ? 'online' : '']"></span
          ><strong>{{
            labels[state.runtime.state] || state.runtime.state
          }}</strong
          ><span class="divider"></span
          ><span>{{ state.runtime.gpu?.name || "Colab GPU" }}</span>
        </div>
        <div class="header-right">
          <span v-if="state.account.logged_in"
            >余额 <b>{{ money(state.account.balance) }}</b> CU</span
          ><button
            class="icon-button"
            aria-label="同步 Google Drive"
            :disabled="!state.setup.drive || !!state.operation"
            @click="act('sync')"
          >
            <PhArrowsClockwise :size="19" />
          </button>
        </div>
      </header>
      <div v-if="error || state.error" class="banner error" role="alert">
        <PhWarningCircle :size="19" /><span>{{ error || state.error }}</span
        ><button
          aria-label="关闭错误"
          @click="
            error = '';
            act('error.clear');
          "
        >
          <PhX :size="18" />
        </button>
      </div>
      <div v-if="state.operation" class="banner operation" role="status">
        <span class="spinner"></span>{{ state.operation.title }}
        <template v-if="state.setup.drive_waiting"
          ><button class="text-button" @click="openURL(state.auth_url)">
            打开授权页</button
          ><button class="text-button" @click="act('drive.skip')">
            稍后连接
          </button></template
        >
      </div>
      <main v-if="page === 'device'" class="page device-page">
        <div class="page-heading">
          <div>
            <h1>远端设备</h1>
            <p>启动一台 GPU，将模型和对话带到云端。</p>
          </div>
          <span class="tag">Google Colab</span>
        </div>
        <section v-if="!state.setup.colab" class="setup-callout">
          <PhGearSix :size="25" />
          <div>
            <h3>连接你的工作空间</h3>
            <p>用 Google 账户登录一次，即可启动 GPU。</p>
          </div>
          <button class="secondary" @click="page = 'settings'">
            前往设置<PhCaretRight :size="16" />
          </button>
        </section>
        <section class="device-panel">
          <div class="device-title">
            <div class="device-icon">
              <PhHardDrives :size="32" weight="duotone" />
            </div>
            <div>
              <h2>{{ state.runtime.gpu?.name || "Colab 远端 GPU" }}</h2>
              <p>{{ state.runtime.id || "尚未分配运行实例" }}</p>
            </div>
            <span :class="['tag', ready ? 'green' : '']">{{
              labels[state.runtime.state]
            }}</span>
          </div>
          <div class="metrics">
            <div>
              <span>显存</span
              ><strong
                >{{
                  state.runtime.gpu
                    ? `${(state.runtime.gpu.used_mib / 1024).toFixed(1)} / ${(state.runtime.gpu.total_mib / 1024).toFixed(0)}`
                    : "未分配"
                }}<small v-if="state.runtime.gpu">GB</small></strong
              >
            </div>
            <div>
              <span>可用磁盘</span
              ><strong>{{
                state.runtime.disk_free
                  ? bytes(state.runtime.disk_free)
                  : "未分配"
              }}</strong>
            </div>
            <div>
              <span>账户当前费率</span
              ><strong
                >{{ money(state.account.rate)
                }}<small v-if="state.account.rate !== undefined"
                  >CU / 小时</small
                ></strong
              >
            </div>
          </div>
          <div class="device-controls">
            <label
              >GPU 型号<select
                v-model="form.gpu"
                :disabled="state.runtime.state !== 'stopped'"
              >
                <option
                  v-for="g in ['T4', 'L4', 'A100', 'G4', 'H100']"
                  :key="g"
                >
                  {{ g }}
                </option>
              </select></label
            >
            <div class="device-policy">
              <span>单次预算 {{ form.max_cu }} CU</span
              ><small>空闲 {{ form.idle_minutes }} 分钟自动释放</small>
            </div>
            <button
              v-if="state.runtime.state === 'stopped'"
              class="primary"
              :disabled="busy || !!state.operation || !state.setup.colab"
              @click="start"
            >
              <PhPlay :size="17" weight="fill" />启动 GPU</button
            ><button
              v-else
              class="danger-outline"
              :disabled="!!state.operation"
              @click="
                showRelease = true;
                force = false;
              "
            >
              <PhStop :size="17" />释放 GPU
            </button>
          </div>
        </section>
        <section class="next-step">
          <div>
            <h3>{{ loaded ? "模型已就绪" : "下一步：准备模型" }}</h3>
            <p>
              {{
                loaded
                  ? loaded.name + " 已加载到远端显存，可以开始对话。"
                  : "设备启动后，在模型库中下载并加载一个模型。"
              }}
            </p>
          </div>
          <button
            class="text-button"
            @click="page = loaded ? 'chat' : 'models'"
          >
            {{ loaded ? "开始聊天" : "打开模型库" }}<PhCaretRight :size="17" />
          </button>
        </section>
        <section
          v-if="state.runtime.sync && state.setup.drive"
          class="sync-note"
        >
          <PhCloud :size="18" /><span>{{
            state.runtime.sync.error ||
            (state.runtime.sync.pending
              ? `${state.runtime.sync.pending} 项等待保存到 Google Drive`
              : "会话与模型配置已写入云盘，释放前将确认上传完成")
          }}</span>
        </section>
        <p class="footnote">
          释放 GPU 会清空这台实例的模型文件。{{
            state.setup.drive
              ? "会话和模型配置已连接云盘保存。"
              : "会话暂存在远端，连接云盘或导出后可保留。"
          }}下次启动后可以重新下载模型。
        </p>
      </main>
      <main v-else-if="page === 'models'" class="page models-page">
        <div class="page-heading">
          <div>
            <h1>模型库</h1>
            <p>下载到当前远端设备，按需加载到显存。</p>
          </div>
          <button
            class="primary"
            :disabled="!ready || !!state.operation"
            @click="showImport = true"
          >
            <PhPlus :size="18" />导入 GGUF
          </button>
        </div>
        <div class="list-toolbar">
          <input
            v-model="search"
            type="search"
            placeholder="查找模型"
            aria-label="查找模型"
          /><span
            >{{ filtered.length }} 个模型 ·
            {{ state.models.filter((m) => m.downloaded).length }} 个已下载</span
          >
        </div>
        <div v-if="!ready" class="inline-note">
          <PhHardDrives :size="18" />先启动远端 GPU，即可下载和加载模型。<button
            class="text-button"
            @click="page = 'device'"
          >
            前往设备
          </button>
        </div>
        <div class="model-list">
          <article v-for="model in filtered" :key="model.id" class="model-row">
            <div class="model-avatar">
              <PhCube :size="24" weight="duotone" />
            </div>
            <div class="model-info">
              <div class="model-title">
                <h3>{{ model.name }}</h3>
                <span v-if="model.loaded" class="tag green">已加载</span
                ><span v-else-if="model.downloaded" class="tag">已下载</span>
              </div>
              <p>{{ model.description }}</p>
              <small
                >{{ bytes(model.size) }} <span>·</span>
                {{
                  model.min_vram_mib
                    ? `建议显存 ${(model.min_vram_mib / 1024).toFixed(0)} GB 起`
                    : "根据模型大小选择 GPU"
                }}</small
              >
            </div>
            <div class="model-actions">
              <button
                v-if="!model.downloaded"
                class="secondary"
                :disabled="!ready || !!task || busy"
                @click="act('model.download', { id: model.id })"
              >
                <PhDownloadSimple :size="17" />下载到远端</button
              ><button
                v-else-if="!model.loaded"
                class="secondary"
                :disabled="!canLoad(model, state) || busy"
                @click="act('model.load', { id: model.id })"
              >
                <PhPlay :size="17" />加载</button
              ><button
                v-else
                class="secondary"
                :disabled="!!task || busy"
                @click="act('model.unload')"
              >
                卸载</button
              ><button
                v-if="model.downloaded && !model.loaded"
                class="icon-button"
                aria-label="删除远端模型文件"
                :disabled="!!task || busy"
                @click="deleteTarget = model"
              >
                <PhTrash :size="19" />
              </button>
            </div>
          </article>
          <div v-if="!filtered.length" class="empty-small">没有匹配的模型</div>
        </div>
      </main>
      <main v-else-if="page === 'chat'" class="chat-layout">
        <aside class="conversation-sidebar">
          <button
            class="secondary new-chat"
            @click="selected = ''"
            :disabled="task?.kind === 'chat'"
          >
            <PhPlus :size="18" />新对话
          </button>
          <p class="sidebar-label">会话</p>
          <button
            v-for="c in state.conversations"
            :key="c.id"
            class="conversation-item"
            :class="{ selected: selected === c.id }"
            @click="selected = c.id"
          >
            <PhChatCircle :size="17" /><span>{{ c.title }}</span>
          </button>
          <p v-if="!state.conversations.length" class="muted empty-small">
            还没有会话
          </p>
        </aside>
        <section class="chat-main">
          <div class="chat-heading">
            <div>
              <strong>{{ loaded?.name || "尚未加载模型" }}</strong><span>{{
                ready ? "远端推理" : "设备未连接"
              }}</span>
            </div>
            <button
              v-if="conversation"
              class="icon-button"
              aria-label="导出对话"
              @click="exportChat"
            >
              <PhDownloadSimple :size="19" />
            </button>
          </div>
          <div class="messages" aria-live="polite">
            <div v-if="!messages.length" class="chat-empty">
              <div class="chat-empty-icon">
                <PhChatCircle :size="36" weight="duotone" />
              </div>
              <h2>从这里开始对话</h2>
              <p>
                {{
                  loaded
                    ? "你的消息将发送到远端 GPU 上运行的模型。"
                    : "启动 GPU，然后在模型库下载并加载模型。"
                }}
              </p>
              <button
                v-if="!loaded"
                class="secondary"
                @click="page = ready ? 'models' : 'device'"
              >
                {{ ready ? "选择模型" : "启动设备" }}
              </button>
            </div>
            <article
              v-for="(message, i) in messages"
              :key="i"
              :class="['message', message.role]"
            >
              <div class="message-label">
                {{
                  message.role === "user"
                    ? "你"
                    : message.model
                      ? state.models.find((m) => m.id === message.model)
                          ?.name || "助手"
                      : "助手"
                }}<span v-if="message.status === 'interrupted'">回答中断</span
                ><span v-if="message.status === 'cancelled'">已停止</span>
              </div>
              <div
                v-if="message.role === 'assistant'"
                class="markdown"
                @click="codeClick"
                v-html="html(message.content)"
              ></div>
              <div v-else class="user-text">{{ message.content }}</div>
              <span
                v-if="message.status === 'streaming'"
                class="typing-dot"
              ></span
              ><button
                v-if="message.content"
                class="copy-button"
                aria-label="复制消息"
                @click="copy(message.content)"
              >
                <PhCopy :size="15" />
              </button>
            </article>
          </div>
          <div class="composer-area">
            <div class="composer">
              <textarea
                v-model="draft"
                rows="2"
                :disabled="!loaded || !ready"
                placeholder="发送消息到远端模型…"
                aria-label="消息"
                @keydown="inputKey"
              ></textarea>
              <div class="composer-bottom">
                <span>{{
                  loaded
                    ? "Enter 发送 · Shift + Enter 换行"
                    : "请先加载远端模型"
                }}</span><button
                  v-if="task?.kind === 'chat'"
                  class="send-button stop"
                  aria-label="停止生成"
                  @click="act('task.cancel', { id: task.id })"
                >
                  <PhStop :size="19" weight="fill" /></button
                ><button
                  v-else
                  class="send-button"
                  aria-label="发送消息"
                  :disabled="
                    !draft.trim() || !loaded || !ready || !!task || busy
                  "
                  @click="send"
                >
                  <PhArrowUp :size="21" weight="bold" />
                </button>
              </div>
            </div>
            <p class="composer-note">
              {{
                state.runtime.sync?.error ||
                (state.setup.drive
                  ? "会话保存到 Google Drive · 模型运行在远端"
                  : "会话暂存在远端 · 释放 GPU 前请导出，或连接云备份")
              }}
            </p>
          </div>
        </section>
      </main>
      <main v-else class="page settings-page">
        <div class="page-heading">
          <div>
            <h1>设置</h1>
            <p>登录一次，就能开始使用远端 GPU。</p>
          </div>
        </div>
        <section class="settings-section">
          <div class="section-heading">
            <span class="step-number"
              ><PhCheck v-if="state.setup.colab" :size="16" /><span v-else
                >1</span
              ></span
            >
            <h2>Google 账户</h2>
            <span v-if="state.setup.colab" class="tag green">已连接</span>
          </div>
          <p>
            {{
              state.account.email ||
              "在浏览器选择 Google 账户并授权，完成后会自动返回这里。"
            }}
          </p>
          <div class="form-row">
            <button
              :class="state.setup.colab ? 'secondary' : 'primary'"
              :disabled="
                busy || !!state.operation || state.runtime.state !== 'stopped'
              "
              @click="beginLogin"
            >
              {{ state.setup.colab ? "重新登录" : "登录 Google"
              }}<PhArrowSquareOut :size="16" />
            </button>
            <button
              v-if="state.setup.colab"
              class="primary"
              @click="page = 'device'"
            >
              去启动 GPU<PhCaretRight :size="16" />
            </button>
            <button
              v-if="state.auth_url && state.operation"
              class="text-button"
              @click="openURL(state.auth_url)"
            >
              重新打开登录页
            </button>
          </div>
          <details class="setup-details" :open="showAuthPaste">
            <summary @click.prevent="showAuthPaste = !showAuthPaste">
              浏览器有一段回显？粘贴到这里
            </summary>
            <div class="details-content">
              <p>
                整段页面文字、完整链接或授权码都可以，客户端会自动提取。请使用本次登录页面的回显。
              </p>
              <div class="form-row">
                <button
                  class="secondary"
                  :disabled="busy || state.runtime.state !== 'stopped'"
                  @click="pasteLogin"
                >
                  <PhCopy :size="16" />粘贴并完成登录
                </button>
                <button
                  class="text-button"
                  :disabled="
                    busy ||
                    !!state.operation ||
                    state.runtime.state !== 'stopped'
                  "
                  @click="manualLogin"
                >
                  改用复制粘贴登录<PhArrowSquareOut :size="15" />
                </button>
              </div>
              <textarea
                v-model="code"
                class="config-input"
                rows="3"
                autocomplete="off"
                autocapitalize="off"
                spellcheck="false"
                placeholder="也可以在这里粘贴浏览器回显的全部内容…"
                aria-label="Google 授权回显"
              ></textarea>
              <button
                class="secondary"
                :disabled="
                  !code.trim() || busy || state.runtime.state !== 'stopped'
                "
                @click="finishLogin"
              >
                自动识别并登录
              </button>
            </div>
          </details>
        </section>
        <section class="settings-section">
          <div class="section-heading">
            <PhCloud :size="22" />
            <h2>云备份</h2>
            <span :class="['tag', state.setup.drive ? 'green' : '']">{{
              state.setup.drive ? "已连接" : "未连接"
            }}</span>
          </div>
          <p>
            将会话和模型清单保存到你的 Google
            Drive，换电脑也能继续。模型文件仍保存在远端 GPU。
          </p>
          <div class="form-row">
            <button
              class="secondary"
              :disabled="busy || !ready || !!state.operation"
              @click="act(state.setup.drive ? 'sync' : 'drive.connect')"
            >
              <PhCloud :size="17" />{{
                state.setup.drive ? "立即同步" : "连接 Google 云盘"
              }}
            </button>
            <button
              v-if="!ready && state.setup.colab"
              class="text-button"
              @click="page = 'device'"
            >
              先启动 GPU<PhCaretRight :size="16" />
            </button>
          </div>
          <p class="footnote">
            {{
              state.setup.drive
                ? "已连接：我的云端硬盘 / ColabOne。释放 GPU 前会等待文件上传完成。"
                : "启动 GPU 后即可连接，浏览器确认授权后会自动完成。新建实例时可能需要再次确认。"
            }}
          </p>
          <label class="checkbox"
            ><input
              type="checkbox"
              v-model="form.auto_drive"
              @change="save"
            />启动 GPU 时自动连接云盘</label
          >
          <p v-if="!state.setup.drive" class="footnote">
            未连接时，会话暂存在远端；释放前可在聊天页导出。
          </p>
        </section>
        <section class="settings-section">
          <h2>使用偏好</h2>
          <p>默认设置即可开始使用。</p>
          <div class="form-grid">
            <label
              >单次 GPU 预算（CU）<input
                v-model.number="form.max_cu"
                type="number"
                min="0.1"
                max="100"
                step="0.1"
            /></label>
            <label
              >空闲自动释放（分钟）<input
                v-model.number="form.idle_minutes"
                type="number"
                min="1"
                max="120"
            /></label>
          </div>
          <p class="footnote">
            预算保护会提前预留释放时间。新的预算从下次启动 GPU 生效。
          </p>
          <details class="setup-details">
            <summary>高级设置</summary>
            <div class="details-content">
              <label
                >Hugging Face Token（可选）<input
                  v-model="form.hf_token"
                  type="password"
                  autocomplete="new-password"
                  :placeholder="
                    state.setup.hf
                      ? '已保存，留空保留'
                      : '只有需要授权的模型才需填写'
                  "
              /></label>
            </div>
          </details>
        </section>
        <div class="settings-footer">
          <span>登录信息保存在系统凭据库</span
          ><button class="primary" :disabled="busy" @click="save">
            保存偏好
          </button>
        </div>
      </main>
      <footer v-if="task && task.kind !== 'chat'" class="task-bar">
        <span class="spinner"></span>
        <div>
          <strong>{{
            task.kind === "download" ? "正在下载到远端" : "正在加载模型"
          }}</strong><small>{{ task.message }}</small
          ><progress
            v-if="task.kind === 'download'"
            :value="task.progress"
            max="1"
          ></progress>
        </div>
        <span v-if="task.kind === 'download'"
          >{{ Math.round(task.progress * 100) }}%</span
        ><button class="secondary" @click="act('task.cancel', { id: task.id })">
          取消
        </button>
      </footer>
      <div
        v-for="failed in (state.runtime.tasks || [])
          .filter((t) => t.state === 'failed')
          .slice(-1)"
        :key="failed.id"
        class="task-error"
        role="alert"
      >
        <PhWarningCircle :size="17" />{{ failed.error }}
      </div>
    </div>
    <div v-if="toast" class="toast" role="status">
      <PhCheck :size="18" />{{ toast }}
    </div>
    <div
      v-if="showImport"
      class="modal-backdrop"
      @click.self="showImport = false"
    >
      <section
        class="modal"
        role="dialog"
        aria-modal="true"
        aria-labelledby="import-title"
      >
        <button
          class="modal-close icon-button"
          aria-label="关闭"
          @click="showImport = false"
        >
          <PhX :size="20" />
        </button>
        <h2 id="import-title">导入 Hugging Face GGUF</h2>
        <p>
          添加模型配置，随后由远端设备下载权重。选择分片文件时会自动包含同组全部分片。
        </p>
        <label
          >模型仓库<input
            v-model="importForm.repo"
            placeholder="Qwen/Qwen2.5-0.5B-Instruct-GGUF" /></label
        ><label
          >GGUF 文件路径<input
            v-model="importForm.filename"
            placeholder="qwen2.5-0.5b-instruct-q4_k_m.gguf" /></label
        ><label
          >Revision<input v-model="importForm.revision" placeholder="main"
        /></label>
        <div class="modal-actions">
          <button class="secondary" @click="showImport = false">取消</button
          ><button
            class="primary"
            :disabled="!importForm.repo || !importForm.filename || busy"
            @click="importModel"
          >
            添加模型
          </button>
        </div>
      </section>
    </div>
    <div v-if="showRelease" class="modal-backdrop">
      <section
        class="modal"
        role="dialog"
        aria-modal="true"
        aria-labelledby="release-title"
      >
        <h2 id="release-title">释放远端 GPU？</h2>
        <p>
          {{
            state.setup.drive
              ? "会先停止任务，等待云盘文件上传完成，再释放当前实例。"
              : "会停止任务并释放当前实例。未连接云备份，请先在聊天页导出需要保留的会话。"
          }}远端模型文件会被清空，下次启动需要重新下载。
        </p>
        <label class="checkbox"
          ><input type="checkbox" v-model="force" />{{
            state.setup.drive
              ? "连接或同步失败时仍然释放，放弃未同步内容"
              : "我已导出或不需要保留远端会话"
          }}</label
        >
        <div class="modal-actions">
          <button class="secondary" @click="showRelease = false">取消</button
          ><button class="danger" :disabled="busy" @click="release">
            释放 GPU
          </button>
        </div>
      </section>
    </div>
    <div v-if="deleteTarget" class="modal-backdrop">
      <section
        class="modal"
        role="dialog"
        aria-modal="true"
        aria-labelledby="delete-title"
      >
        <h2 id="delete-title">删除远端模型文件？</h2>
        <p>{{ deleteTarget.name }} 的配置和历史会话会保留。</p>
        <div class="modal-actions">
          <button class="secondary" @click="deleteTarget = null">取消</button
          ><button
            class="danger"
            @click="
              act('model.delete', { id: deleteTarget.id });
              deleteTarget = null;
            "
          >
            删除文件
          </button>
        </div>
      </section>
    </div>
  </div>
</template>
