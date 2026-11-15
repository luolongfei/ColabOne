<script setup lang="ts">
import { computed, onUnmounted, ref } from "vue";
import {
  PhCopy,
  PhGlobe,
  PhKey,
  PhPlay,
  PhStop,
  PhArrowRight,
  PhEye,
  PhEyeSlash,
  PhArrowSquareOut,
} from "@phosphor-icons/vue";
import { command, openURL, writeClipboard } from "./api";
import type { Snapshot } from "./types";
const props = defineProps<{ state: Snapshot }>();
const emit = defineEmits<{
  device: [];
  run: [];
  image: [];
  video: [];
  error: [string];
}>();
const mode = ref(props.state.api_settings?.mode || "quick");
const domain = ref(props.state.api_settings?.domain || "");
const tunnelToken = ref("");
const key = ref("");
const keyTimer = ref<ReturnType<typeof setTimeout>>();
const busy = ref(false),
  notice = ref(""),
  example = ref("chat"),
  language = ref("curl"),
  rotating = ref(false);
const api = computed(() => props.state.runtime.public_api);
const ready = computed(() => props.state.runtime.state === "ready");
const enabled = computed(
  () => !!api.value?.enabled || api.value?.state === "starting",
);
const model = computed(() =>
  props.state.models.find((m) => m.id === props.state.runtime.loaded_model),
);
const status = computed(
  () =>
    ({
      stopping: "正在关闭",
      stopped: ready.value
        ? "未开启"
        : props.state.runtime.state === "disconnected"
          ? "状态待确认"
          : "未开启",
      starting: "正在连接",
      ready: "已开启",
      reconnecting: "连接待恢复",
      failed: "连接失败",
    })[api.value?.state || "stopped"] || "未开启",
);
const base = computed(() =>
  api.value?.url ? `${api.value.url}/v1` : "https://YOUR-API-HOST/v1",
);
const selectedModel = computed(() =>
  model.value?.kind === example.value ||
  (!model.value?.kind && example.value === "chat")
    ? model.value?.id
    : example.value === "chat"
      ? "YOUR-CHAT-MODEL"
      : `YOUR-${example.value.toUpperCase()}-MODEL`,
);
const streaming = computed(() =>
  api.value?.enabled ? api.value.mode === "named" : mode.value === "named",
);
const code = computed(() => {
  const id = selectedModel.value;
  if (example.value === "chat") {
    if (language.value === "python")
      return `import os\nfrom openai import OpenAI\n\nclient = OpenAI(\n    base_url="${base.value}",\n    api_key=os.environ["COLABONE_API_KEY"],\n)\nreply = client.chat.completions.create(\n    model="${id}",\n    messages=[{"role": "user", "content": "你好"}],\n    max_tokens=512,\n    stream=${streaming.value ? "True" : "False"},\n)\n${streaming.value ? 'for chunk in reply:\n    if chunk.choices:\n        print(chunk.choices[0].delta.content or "", end="", flush=True)' : "print(reply.choices[0].message.content)"}`;
    return `curl ${streaming.value ? "-N " : ""}"${base.value}/chat/completions" \\\n  -H "Authorization: Bearer $COLABONE_API_KEY" \\\n  -H "Content-Type: application/json" \\\n  -d '${JSON.stringify({ model: id, messages: [{ role: "user", content: "你好" }], max_tokens: 512, stream: streaming.value }, null, 2)}'`;
  }
  const video = example.value === "video";
  const payload = {
    model_id: id,
    kind: example.value,
    prompt: video
      ? "云朵缓缓飘过山谷，镜头平稳向前"
      : "山间湖泊，晨光，写实摄影",
    width: video ? 832 : 1024,
    height: video ? 480 : 1024,
    ...(video ? { frames: 33, fps: 16 } : {}),
  };
  if (language.value === "python")
    return `import os, time, requests\n\nbase = "${base.value}"\nheaders = {"Authorization": "Bearer " + os.environ["COLABONE_API_KEY"]}\nresponse = requests.post(base + "/jobs", headers=headers, json=${JSON.stringify(payload, null, 4)}, timeout=30)\nresponse.raise_for_status()\njob = response.json()\nwhile job["status"] == "running":\n    time.sleep(2)\n    response = requests.get(base + "/jobs/" + job["id"], headers=headers, timeout=30)\n    response.raise_for_status()\n    job = response.json()\n    print(job["status"], job["progress"])\nif job["status"] != "completed":\n    raise RuntimeError(job.get("error") or job["status"])\nfor index, output in enumerate(job["outputs"]):\n    response = requests.get(base.removesuffix("/v1") + output["content_path"], headers=headers, timeout=60)\n    response.raise_for_status()\n    with open(f"result-{index}.${video ? "webm" : "png"}", "wb") as file:\n        file.write(response.content)`;
  return `# 1. 提交任务，记录返回的 id\ncurl "${base.value}/jobs" \\\n  -H "Authorization: Bearer $COLABONE_API_KEY" \\\n  -H "Content-Type: application/json" \\\n  -d '${JSON.stringify(payload, null, 2)}'\n\n# 2. 查询进度（将 JOB_ID 替换为返回的 id）\ncurl "${base.value}/jobs/JOB_ID" \\\n  -H "Authorization: Bearer $COLABONE_API_KEY"\n\n# 3. 完成后，使用 outputs 中的 content_path 下载结果\ncurl "${base.value}/jobs/JOB_ID/outputs/OUTPUT_ID" \\\n  -H "Authorization: Bearer $COLABONE_API_KEY" \\\n  -o result.${video ? "webm" : "png"}`;
});
async function act(action: string, payload = {}) {
  busy.value = true;
  notice.value = "";
  try {
    await command(action, payload);
    return true;
  } catch (e) {
    emit("error", String(e));
    return false;
  } finally {
    busy.value = false;
  }
}
async function start() {
  if (!ready.value) return emit("device");
  if ((props.state.runtime.protocol || 0) < 4) {
    await act("runtime.upgrade");
    return;
  }
  if (
    await act("api.start", {
      mode: mode.value,
      domain: domain.value,
      tunnel_token: tunnelToken.value,
    })
  ) {
    tunnelToken.value = "";
    notice.value = "正在远端开启，请稍候。公网验证通过后会显示调用地址。";
  }
}
async function save() {
  if (
    await act("api.save", {
      mode: mode.value,
      domain: domain.value,
      tunnel_token: tunnelToken.value,
    })
  ) {
    tunnelToken.value = "";
    notice.value = "连接配置已保存到本机凭据库。";
  }
}
async function copy(value: string) {
  try {
    await writeClipboard(value);
    notice.value = "已复制";
  } catch {
    emit("error", "复制失败，请手动选择并复制");
  }
}
async function copyKey() {
  try {
    const value = await command("api.key");
    await copy(value.key);
  } catch (e) {
    emit("error", String(e));
  }
}
async function reveal() {
  if (key.value) {
    key.value = "";
    return;
  }
  try {
    key.value = (await command("api.key")).key;
    clearTimeout(keyTimer.value);
    keyTimer.value = setTimeout(() => (key.value = ""), 30000);
  } catch (e) {
    emit("error", String(e));
  }
}
async function rotate() {
  if (await act("api.rotate")) {
    key.value = "";
    notice.value = "已更换密钥。请重新开启 API，并更新调用方保存的密钥。";
    rotating.value = false;
  }
}
onUnmounted(() => {
  key.value = "";
  clearTimeout(keyTimer.value);
});
</script>
<template>
  <main class="page api-page">
    <div class="page-heading">
      <div>
        <h1>API 服务</h1>
        <p>让其他应用调用你的远端模型。</p>
      </div>
      <span class="badge" :class="{ green: api?.state === 'ready' }"
        ><span
          class="status-dot"
          :class="{ online: api?.state === 'ready' }"
        />{{ status }}</span
      >
    </div>
    <section class="api-overview run-card">
      <div class="api-overview-title">
        <div class="api-symbol"><PhGlobe :size="28" /></div>
        <div>
          <h2>远端模型，对外可用</h2>
          <p>{{ model?.name || "尚未加载模型" }}</p>
        </div>
        <button class="text-button" @click="emit('run')">
          管理运行模型 <PhArrowRight />
        </button>
      </div>
      <dl class="run-stats">
        <div>
          <dt>累计生成请求</dt>
          <dd>{{ api?.requests || 0 }}</dd>
        </div>
        <div>
          <dt>正在处理</dt>
          <dd>{{ api?.active || 0 }}</dd>
        </div>
        <div>
          <dt>已完成 / 失败或取消</dt>
          <dd>{{ api?.completed || 0 }} / {{ api?.failed || 0 }}</dd>
        </div>
      </dl>
      <p v-if="api?.error" class="api-error" role="alert">{{ api.error }}</p>
      <p class="field-help">
        模型下载、加载和计算均在远端 GPU。API
        调用期间暂停闲置计时；无生成请求和操作满 3 分钟后，再倒计时 60
        秒释放。预算上限始终有效。
      </p>
    </section>
    <div class="api-grid">
      <section class="run-card api-connection">
        <h2>连接方式</h2>
        <div class="option-pills">
          <button
            :disabled="enabled || busy"
            :aria-pressed="mode === 'quick'"
            @click="mode = 'quick'"
          >
            临时地址</button
          ><button
            :disabled="enabled || busy"
            :aria-pressed="mode === 'named'"
            @click="mode = 'named'"
          >
            固定域名
          </button>
        </div>
        <p class="field-help" v-if="mode === 'quick'">
          无需 Cloudflare 账号，一键生成 HTTPS
          地址。每次开启地址会变化；聊天请关闭流式输出（stream=false）。
        </p>
        <template v-else>
          <p class="field-help">
            适合长期接入，支持流式聊天。域名与隧道保存在你的 Cloudflare 账号中。
          </p>
          <label for="api-domain">API 域名</label
          ><input
            id="api-domain"
            v-model="domain"
            :disabled="enabled || busy"
            placeholder="api.example.com"
            autocomplete="off"
            spellcheck="false"
          />
          <label for="api-tunnel">Cloudflare 安装命令或 Tunnel Token</label
          ><textarea
            id="api-tunnel"
            v-model="tunnelToken"
            :disabled="enabled || busy"
            rows="2"
            :placeholder="
              state.api_settings?.has_token
                ? '已安全保存；留空继续使用'
                : '直接粘贴整段安装命令，自动提取 Token'
            "
            autocomplete="off"
            spellcheck="false"
          />
          <details class="api-guide">
            <summary>第一次连接？查看 3 步设置</summary>
            <ol>
              <li>
                在 Cloudflare 创建一个 Tunnel，选择 Cloudflared。<button
                  class="text-button"
                  @click="openURL('https://one.dash.cloudflare.com/')"
                >
                  打开控制台 <PhArrowSquareOut />
                </button>
              </li>
              <li>
                添加一个公开域名，服务类型选 HTTP，地址填
                <code>127.0.0.1:7861</code>。
              </li>
              <li>
                将 Cloudflare
                给出的安装命令粘贴到上方，点击开启。客户端会在远端安装并验证连接。
              </li>
            </ol>
            <p class="field-help">
              供程序调用的域名请使用 API Key 鉴权；交互式登录页面会阻挡 API
              客户端。
            </p>
          </details>
        </template>
        <div class="run-actions">
          <button
            v-if="enabled"
            class="danger solid"
            :disabled="busy || !!state.operation"
            @click="act('api.stop')"
          >
            <PhStop weight="fill" />关闭对外 API</button
          ><button
            v-else
            class="primary"
            :disabled="busy || !!state.operation"
            @click="start"
          >
            <PhPlay weight="fill" />{{
              !ready
                ? "前往远端设备"
                : (state.runtime.protocol || 0) < 4
                  ? "更新远端服务"
                  : "开启 API 服务"
            }}</button
          ><button
            v-if="!enabled"
            class="secondary"
            :disabled="busy"
            @click="save"
          >
            保存配置
          </button>
        </div>
      </section>
      <section class="run-card api-access">
        <h2>接入信息</h2>
        <label>Base URL</label>
        <div class="api-copy-row">
          <code>{{ api?.url ? base : "开启后自动生成" }}</code><button
            class="icon-button"
            aria-label="复制 API 地址"
            :disabled="!api?.url || api.state !== 'ready'"
            @click="copy(base)"
          >
            <PhCopy />
          </button>
        </div>
        <label>API Key</label>
        <div class="api-copy-row">
          <code>{{ key || "••••••••••••••••••••••••" }}</code><button
            class="icon-button"
            :aria-label="key ? '隐藏 API 密钥' : '显示 API 密钥'"
            @click="reveal"
          >
            <PhEyeSlash v-if="key" /><PhEye v-else /></button
          ><button
            class="icon-button"
            aria-label="复制 API 密钥"
            @click="copyKey"
          >
            <PhCopy />
          </button>
        </div>
        <p class="field-help">
          <PhKey /> 密钥保存在本机系统凭据库。调用时使用 Authorization:
          Bearer，只分享给需要调用模型的人。
        </p>
        <label>当前模型 ID</label>
        <div class="api-copy-row">
          <code>{{ model?.id || "请先加载模型" }}</code><button
            class="icon-button"
            aria-label="复制模型 ID"
            :disabled="!model"
            @click="copy(model!.id)"
          >
            <PhCopy />
          </button>
        </div>
        <button
          v-if="!rotating"
          class="text-button api-rotate"
          @click="rotating = true"
        >
          更换 API Key
        </button>
        <div v-else class="api-rotate">
          <p>将关闭对外 API 并使旧密钥失效，调用方需要更新密钥。</p>
          <button class="danger" :disabled="busy" @click="rotate">
            确认更换
          </button>
          <button class="secondary" @click="rotating = false">取消</button>
        </div>
      </section>
    </div>
    <p v-if="notice" class="inline-note" role="status">{{ notice }}</p>
    <section class="run-card api-examples">
      <div class="api-example-heading">
        <div>
          <h2>开始调用</h2>
          <p class="field-help">
            先将密钥设置为环境变量 COLABONE_API_KEY，再复制示例。
          </p>
        </div>
        <button class="secondary" @click="copy(code)">
          <PhCopy />复制代码
        </button>
      </div>
      <div class="api-example-tabs">
        <div class="option-pills">
          <button
            v-for="item in [
              { id: 'chat', label: '聊天' },
              { id: 'image', label: '画图' },
              { id: 'video', label: '视频' },
            ]"
            :key="item.id"
            :aria-pressed="example === item.id"
            @click="example = item.id"
          >
            {{ item.label }}
          </button>
        </div>
        <div class="option-pills">
          <button
            v-for="item in ['curl', 'python']"
            :key="item"
            :aria-pressed="language === item"
            @click="language = item"
          >
            {{ item === "python" ? "Python" : "cURL" }}
          </button>
        </div>
      </div>
      <p
        v-if="
          model &&
          model.kind !== example &&
          !(example === 'chat' && !model.kind)
        "
        class="inline-note"
      >
        当前加载的是{{
          model.kind === "video"
            ? "视频"
            : model.kind === "image"
              ? "画图"
              : "聊天"
        }}模型。调用此接口前，请切换到相应模型。
      </p>
      <pre tabindex="0"><code>{{ code }}</code></pre>
      <p class="field-help">
        聊天兼容 OpenAI Chat Completions
        的文本消息与常用采样参数；暂不支持工具调用、视觉输入和 Responses
        API。画图和视频使用 ColabOne
        异步任务接口，生成期间可查询进度和取消。一次运行一个生成任务，繁忙时返回
        409。
      </p>
      <details class="api-guide">
        <summary>接口列表与限制</summary>
        <ul>
          <li><code>GET /v1/models</code>：当前已加载模型。</li>
          <li>
            <code>POST /v1/chat/completions</code>：文本聊天；固定域名支持
            stream=true。
          </li>
          <li>
            <code>POST /v1/images/generations</code>：兼容图片生成，返回
            b64_json，等待上限 90 秒；较慢的模型请使用异步接口。
          </li>
          <li>
            <code>POST /v1/jobs</code>：异步画图 / 视频；GET /v1/jobs/{id}
            查询，DELETE /v1/jobs/{id} 取消。
          </li>
          <li>
            <code>GET /v1/jobs/{id}/outputs/{output_id}</code>：下载本任务生成的
            PNG / WebM。
          </li>
        </ul>
        <p class="field-help">
          任务记录保留最近 100 条，GPU
          释放后失效。作品同时保存在客户端作品列表，连接云盘后自动备份。停止 API
          不会释放 GPU；“停止全部”会停止实例与 API。
        </p>
      </details>
    </section>
  </main>
</template>
