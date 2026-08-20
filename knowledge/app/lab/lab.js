const $ = (selector) => document.querySelector(selector);
const $$ = (selector) => Array.from(document.querySelectorAll(selector));

const elements = {
  form: $("#upload-form"),
  fileInput: $("#file-input"),
  dropZone: $("#drop-zone"),
  dropTitle: $("#drop-title"),
  dropCopy: $("#drop-copy"),
  selectedFile: $("#selected-file"),
  uploadButton: $("#upload-button"),
  formMessage: $("#form-message"),
  currentStatus: $("#current-status"),
  currentFilename: $("#current-filename"),
  currentStage: $("#current-stage"),
  currentProgress: $("#current-progress"),
  progressFill: $("#progress-fill"),
  pipelineTrack: $("#pipeline-track"),
  errorBox: $("#error-box"),
  errorCode: $("#error-code"),
  errorMessage: $("#error-message"),
  retryButton: $("#retry-button"),
  refreshButton: $("#refresh-button"),
  jobList: $("#job-list"),
  artifactContext: $("#artifact-context"),
  artifactSummary: $("#artifact-summary"),
  cockpitLink: $("#cockpit-link"),
  artifactViewer: $("#artifact-viewer code"),
  loginBar: $("#login-bar"),
  loginUser: $("#login-user"),
  loginPass: $("#login-pass"),
  loginButton: $("#login-button"),
  loginStatus: $("#login-status"),
  askForm: $("#ask-form"),
  askInput: $("#ask-input"),
  askButton: $("#ask-button"),
  askMode: $("#ask-mode"),
  askMessage: $("#ask-message"),
  askResult: $("#ask-result"),
  askAnswerText: $("#ask-answer-text"),
  askCitationList: $("#ask-citation-list"),
};

const pipelineSteps = ["queued", "downloading", "parsing", "saving_artifacts", "chunking", "embedding", "indexing", "finalizing"];
const pipelineProgress = [0, 10, 30, 55, 68, 78, 90, 97];
const stageLabels = {
  queued: "等待执行", recovered: "恢复排队", starting: "启动任务", downloading: "读取 RustFS",
  parsing: "解析与清洗", saving_artifacts: "保存派生产物", chunking: "文本切块",
  embedding: "生成向量", indexing: "写入索引", finalizing: "生成 Manifest",
  completed: "处理完成", failed: "处理失败",
};
const state = { jobs: [], selectedId: null, artifact: "clean.md", uploading: false, asking: false };

// ---------------------------------------------------------------------------
// 登录态：令牌存 localStorage；AUTH_ENABLED=true 时业务请求携带 Bearer
// ---------------------------------------------------------------------------
const auth = {
  token: localStorage.getItem("es_token") || "",
  username: localStorage.getItem("es_username") || "",
};

function apiFetch(url, options = {}) {
  const headers = new Headers(options.headers || {});
  if (auth.token) headers.set("Authorization", `Bearer ${auth.token}`);
  return fetch(url, { ...options, headers });
}

function showLoginBar(message = "") {
  elements.loginBar.hidden = false;
  elements.loginStatus.textContent = message;
}

function handleAuthFailure() {
  auth.token = "";
  auth.username = "";
  localStorage.removeItem("es_token");
  localStorage.removeItem("es_username");
  showLoginBar("请登录后使用");
}

async function doLogin() {
  const username = elements.loginUser.value.trim();
  const password = elements.loginPass.value;
  if (!username || !password) return;
  elements.loginButton.disabled = true;
  elements.loginStatus.textContent = "登录中…";
  try {
    const response = await fetch("/auth/login", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ username, password }),
    });
    if (!response.ok) throw new Error((await parseError(response)) || "登录失败");
    const payload = await response.json();
    auth.token = payload.token;
    auth.username = payload.username;
    localStorage.setItem("es_token", payload.token);
    localStorage.setItem("es_username", payload.username);
    elements.loginBar.hidden = true;
    elements.loginStatus.textContent = "";
    setFormMessage(`已登录：${payload.username}`, false);
    await refreshJobs();
  } catch (error) {
    elements.loginStatus.textContent = error.message || "登录失败";
  } finally {
    elements.loginButton.disabled = false;
  }
}

function formatBytes(bytes) {
  if (!Number.isFinite(bytes)) return "";
  const units = ["B", "KB", "MB", "GB"];
  let value = bytes;
  let index = 0;
  while (value >= 1024 && index < units.length - 1) { value /= 1024; index += 1; }
  return `${value.toFixed(index ? 1 : 0)} ${units[index]}`;
}

function formatTime(value) {
  if (!value) return "—";
  return new Intl.DateTimeFormat("zh-CN", { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", second: "2-digit" }).format(new Date(value));
}

function setSelectedFile(file) {
  if (!file) {
    elements.selectedFile.textContent = "尚未选择文件";
    elements.dropTitle.textContent = "拖入文件，或点击选择";
    elements.dropCopy.textContent = "Office 常用格式优先；大小上限由服务端 MAX_SOURCE_BYTES 控制";
    elements.uploadButton.disabled = true;
    return;
  }
  elements.selectedFile.textContent = `${file.name} · ${formatBytes(file.size)}`;
  elements.dropTitle.textContent = file.name;
  elements.dropCopy.textContent = `${formatBytes(file.size)} · 点击可重新选择`;
  elements.uploadButton.disabled = false;
}

function setFormMessage(message, isError = false) {
  elements.formMessage.textContent = message;
  elements.formMessage.classList.toggle("is-error", isError);
}

async function parseError(response) {
  try {
    const payload = await response.json();
    return payload.detail || JSON.stringify(payload);
  } catch (_) {
    return `${response.status} ${response.statusText}`;
  }
}

async function uploadFile(file) {
  if (!file || state.uploading) return;
  state.uploading = true;
  elements.uploadButton.disabled = true;
  elements.uploadButton.querySelector("span:first-child").textContent = "正在写入与排队…";
  setFormMessage("源文件正在写入 RustFS，请保持服务运行。", false);
  const body = new FormData();
  body.append("file", file);
  try {
    const response = await apiFetch("/lab/api/uploads", { method: "POST", body });
    if (response.status === 401) { handleAuthFailure(); throw new Error("请先登录"); }
    if (!response.ok) throw new Error(await parseError(response));
    const job = await response.json();
    state.selectedId = job.id;
    setFormMessage("上传成功，处理任务已进入流水线。", false);
    elements.fileInput.value = "";
    setSelectedFile(null);
    await refreshJobs();
  } catch (error) {
    setFormMessage(error.message || "上传失败", true);
  } finally {
    state.uploading = false;
    elements.uploadButton.querySelector("span:first-child").textContent = "写入 RustFS 并处理";
    elements.uploadButton.disabled = !elements.fileInput.files.length;
  }
}

function stageIndex(job) {
  if (job.status === "completed") return pipelineSteps.length;
  if (job.stage === "starting" || job.stage === "recovered") return 0;
  const index = pipelineSteps.indexOf(job.stage);
  if (index >= 0) return index;
  if (job.status === "failed") {
    return pipelineProgress.reduce(
      (current, threshold, progressIndex) => job.progress >= threshold ? progressIndex : current,
      0,
    );
  }
  return 0;
}

function renderPipeline(job) {
  if (!job) {
    elements.currentStatus.textContent = "等待任务";
    elements.currentStatus.className = "status-badge is-idle";
    elements.currentFilename.textContent = "—";
    elements.currentStage.textContent = "idle";
    elements.currentProgress.textContent = "0%";
    elements.progressFill.style.width = "0%";
    elements.errorBox.hidden = true;
    $$("#pipeline-track li").forEach((item) => item.className = "");
    return;
  }
  elements.currentStatus.textContent = job.status;
  elements.currentStatus.className = `status-badge is-${job.status}`;
  elements.currentFilename.textContent = job.filename;
  elements.currentStage.textContent = stageLabels[job.stage] || job.stage;
  elements.currentProgress.textContent = `${job.progress}%`;
  elements.progressFill.style.width = `${job.progress}%`;

  const currentIndex = stageIndex(job);
  $$("#pipeline-track li").forEach((item, index) => {
    item.className = "";
    if (job.status === "failed" && index === currentIndex) item.classList.add("is-failed");
    else if (index < currentIndex || job.status === "completed") item.classList.add("is-done");
    else if (index === currentIndex && job.status !== "queued") item.classList.add("is-current");
  });

  elements.errorBox.hidden = job.status !== "failed";
  elements.errorCode.textContent = job.error_code || "PROCESSING_FAILED";
  elements.errorMessage.textContent = job.error_message || "任务未提供详细错误信息。";
}

function createJobItem(job) {
  const button = document.createElement("button");
  button.type = "button";
  button.className = "job-item";
  button.classList.toggle("is-selected", job.id === state.selectedId);
  button.addEventListener("click", () => selectJob(job.id));

  const dot = document.createElement("span");
  dot.className = `job-state ${job.status}`;
  dot.setAttribute("aria-hidden", "true");

  const copy = document.createElement("span");
  copy.className = "job-copy";
  const title = document.createElement("strong");
  title.textContent = job.filename;
  const meta = document.createElement("span");
  meta.textContent = `${formatTime(job.created_at)} · ${stageLabels[job.stage] || job.stage}`;
  copy.append(title, meta);

  const progress = document.createElement("span");
  progress.className = "job-progress";
  progress.textContent = job.status === "failed" ? "FAIL" : `${job.progress}%`;
  button.append(dot, copy, progress);
  return button;
}

function renderJobs() {
  elements.jobList.replaceChildren();
  if (!state.jobs.length) {
    const empty = document.createElement("p");
    empty.className = "empty-state";
    empty.textContent = "还没有任务。上传一个测试文档开始观察。";
    elements.jobList.append(empty);
    renderPipeline(null);
    return;
  }
  state.jobs.forEach((job) => elements.jobList.append(createJobItem(job)));
  const selected = state.jobs.find((job) => job.id === state.selectedId) || state.jobs[0];
  state.selectedId = selected.id;
  renderPipeline(selected);
  if (selected.status === "completed") {
    elements.artifactContext.textContent = `${selected.file_id} / ${selected.version_id}`;
  }
}

async function refreshJobs() {
  try {
    const response = await fetch("/lab/api/jobs?limit=30", { cache: "no-store" });
    if (!response.ok) throw new Error(await parseError(response));
    state.jobs = await response.json();
    if (!state.selectedId && state.jobs.length) state.selectedId = state.jobs[0].id;
    renderJobs();
    const selected = state.jobs.find((job) => job.id === state.selectedId);
    if (selected?.status === "completed" && elements.artifactViewer.dataset.jobId !== selected.id) {
      await Promise.all([loadArtifact(state.artifact), loadAcceptance(selected)]);
    }
  } catch (error) {
    elements.jobList.replaceChildren();
    const message = document.createElement("p");
    message.className = "empty-state";
    message.textContent = `任务读取失败：${error.message}`;
    elements.jobList.append(message);
  }
}

async function selectJob(jobId) {
  state.selectedId = jobId;
  renderJobs();
  const selected = state.jobs.find((job) => job.id === jobId);
  if (selected?.status === "completed") {
    await Promise.all([loadArtifact(state.artifact), loadAcceptance(selected)]);
  } else {
    elements.artifactContext.textContent = selected ? "任务尚未完成" : "选择已完成任务";
    elements.artifactViewer.textContent = "派生产物将在任务完成后可用。";
    delete elements.artifactViewer.dataset.jobId;
    renderAcceptanceSummary(null);
  }
}

async function loadArtifact(name) {
  state.artifact = name;
  const selected = state.jobs.find((job) => job.id === state.selectedId);
  if (!selected || selected.status !== "completed") return;
  $$(".artifact-tab").forEach((tab) => {
    const active = tab.dataset.artifact === name;
    tab.classList.toggle("is-active", active);
    tab.setAttribute("aria-selected", String(active));
  });
  elements.artifactViewer.textContent = `正在读取 ${name}…`;
  try {
    const url = `/documents/${encodeURIComponent(selected.file_id)}/versions/${encodeURIComponent(selected.version_id)}/artifacts/${encodeURIComponent(name)}`;
    const response = await apiFetch(url, { cache: "no-store" });
    if (response.status === 401) { handleAuthFailure(); throw new Error("请先登录"); }
    if (!response.ok) throw new Error(await parseError(response));
    const text = await response.text();
    if (name.endsWith(".json")) {
      try { elements.artifactViewer.textContent = JSON.stringify(JSON.parse(text), null, 2); }
      catch (_) { elements.artifactViewer.textContent = text; }
    } else {
      elements.artifactViewer.textContent = text;
    }
    elements.artifactViewer.dataset.jobId = selected.id;
    elements.artifactContext.textContent = `${selected.file_id} / ${selected.version_id}`;
  } catch (error) {
    elements.artifactViewer.textContent = `读取失败：${error.message}`;
  }
}

function parserLabel(parsing) {
  if (!parsing || typeof parsing !== "object") return null;
  let label;
  if (parsing.provider === "pdf-inspector") label = "pdf-inspector 本地";
  else if (parsing.provider === "mineru") label = `MinerU（${parsing.backend || "pipeline"}）`;
  else label = "本地管线";
  if (parsing.fallback_reason) label += " · 有回退";
  return label;
}

function renderAcceptanceSummary(manifest) {
  const box = elements.artifactSummary;
  if (!manifest) {
    box.hidden = true;
    box.replaceChildren();
    return;
  }
  const chips = [];
  const parser = parserLabel(manifest.parsing);
  if (parser) chips.push(["解析器", parser]);
  chips.push(["入库时间", formatTime(manifest.processed_at)]);
  if (Number.isFinite(manifest.blocks)) chips.push(["结构块", `${manifest.blocks}`]);
  if (Number.isFinite(manifest.chunks)) chips.push(["切块", `${manifest.chunks}`]);
  if (Number.isFinite(manifest.characters)) chips.push(["字符", manifest.characters.toLocaleString("zh-CN")]);
  if (Array.isArray(manifest.warnings) && manifest.warnings.length) chips.push(["警告", `${manifest.warnings.length} 条`]);

  box.replaceChildren(...chips.map(([label, value]) => {
    const chip = document.createElement("span");
    chip.className = "summary-chip";
    const key = document.createElement("em");
    key.textContent = label;
    const val = document.createElement("strong");
    val.textContent = value;
    chip.append(key, val);
    return chip;
  }));
  box.hidden = false;
}

async function loadAcceptance(job) {
  if (!job || job.status !== "completed") {
    renderAcceptanceSummary(null);
    elements.cockpitLink.hidden = true;
    return;
  }
  elements.cockpitLink.hidden = false;
  elements.cockpitLink.href = `/lab/cockpit?doc=${encodeURIComponent(job.file_id)}`;
  try {
    const url = `/documents/${encodeURIComponent(job.file_id)}/versions/${encodeURIComponent(job.version_id)}/artifacts/manifest.json`;
    const response = await apiFetch(url, { cache: "no-store" });
    if (response.status === 401) { handleAuthFailure(); return; }
    if (!response.ok) throw new Error(await parseError(response));
    renderAcceptanceSummary(await response.json());
  } catch (_) {
    renderAcceptanceSummary(null);
  }
}

async function retrySelected() {
  const selected = state.jobs.find((job) => job.id === state.selectedId);
  if (!selected || selected.status !== "failed") return;
  elements.retryButton.disabled = true;
  try {
    const response = await fetch(`/jobs/${encodeURIComponent(selected.id)}/retry`, { method: "POST" });
    if (!response.ok) throw new Error(await parseError(response));
    await refreshJobs();
  } catch (error) {
    elements.errorMessage.textContent = `重试失败：${error.message}`;
  } finally {
    elements.retryButton.disabled = false;
  }
}

function setAskMessage(message, isError = false) {
  elements.askMessage.textContent = message;
  elements.askMessage.classList.toggle("is-error", isError);
}

async function detectAskMode() {
  try {
    const response = await fetch("/health", { cache: "no-store" });
    if (!response.ok) throw new Error();
    const health = await response.json();
    if (health.auth && !auth.token) showLoginBar("服务已开启登录");
    const watched = health.watch_dirs ? ` · 监听 ${health.watch_dirs} 个目录` : "";
    if (health.llm === "configured") {
      elements.askMode.textContent = `生成模式 · ${health.embedder} · ${health.records} 条索引${watched}`;
    } else {
      elements.askMode.textContent = `纯检索模式（未配置 LLM）· ${health.records} 条索引${watched}`;
    }
  } catch (_) {
    elements.askMode.textContent = "服务能力未知";
  }
}

function createCitationItem(context, index) {
  const item = document.createElement("li");
  item.className = "citation-item";

  const head = document.createElement("div");
  head.className = "citation-head";
  const label = document.createElement("strong");
  label.textContent = `[${index + 1}] ${context.filename || context.doc_id || "未知来源"}`;
  const score = document.createElement("span");
  score.className = "citation-score";
  score.textContent = Number.isFinite(context.score) ? `score ${context.score.toFixed(3)}` : "";
  head.append(label, score);
  if (context.ingested_at) {
    const when = document.createElement("span");
    when.className = "citation-time";
    when.title = "文档入库时间（判断内容新旧）";
    when.textContent = `文档时间 ${formatTime(context.ingested_at)}`;
    head.append(when);
  }

  const text = document.createElement("p");
  text.className = "citation-text";
  text.textContent = context.text;

  item.append(head, text);
  if (context.file_id && context.version_id) {
    const link = document.createElement("a");
    link.className = "citation-link";
    // GET 引用链接支持 ?token= 直开（Authorization 头无法用于新标签页）
    const tokenQuery = auth.token ? `?token=${encodeURIComponent(auth.token)}` : "";
    link.href = `/documents/${encodeURIComponent(context.file_id)}/versions/${encodeURIComponent(context.version_id)}/artifacts/clean.md${tokenQuery}`;
    link.target = "_blank";
    link.rel = "noopener";
    link.textContent = `查看 clean.md（${context.file_id} / ${context.version_id}）↗`;
    item.append(link);
  }
  return item;
}

async function askQuestion(question) {
  if (!question || state.asking) return;
  state.asking = true;
  elements.askButton.disabled = true;
  elements.askButton.querySelector("span:first-child").textContent = "检索与生成中…";
  setAskMessage("正在检索知识库…", false);
  try {
    const response = await apiFetch("/query", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ question, top_k: 5 }),
    });
    if (response.status === 401) { handleAuthFailure(); throw new Error("请先登录"); }
    if (!response.ok) throw new Error(await parseError(response));
    const payload = await response.json();
    elements.askAnswerText.textContent = payload.answer || "（无回答）";
    elements.askCitationList.replaceChildren(
      ...(payload.contexts || []).map((context, index) => createCitationItem(context, index)),
    );
    elements.askResult.hidden = false;
    if (!(payload.contexts || []).length) {
      setAskMessage("知识库中没有命中相关片段；请先在上方上传并处理文档。", false);
    } else {
      setAskMessage("", false);
    }
  } catch (error) {
    setAskMessage(error.message || "查询失败", true);
  } finally {
    state.asking = false;
    elements.askButton.querySelector("span:first-child").textContent = "提问";
    elements.askButton.disabled = !elements.askInput.value.trim();
  }
}

elements.fileInput.addEventListener("change", () => setSelectedFile(elements.fileInput.files[0]));
elements.form.addEventListener("submit", (event) => { event.preventDefault(); uploadFile(elements.fileInput.files[0]); });
elements.dropZone.addEventListener("keydown", (event) => {
  if (event.key === "Enter" || event.key === " ") { event.preventDefault(); elements.fileInput.click(); }
});
["dragenter", "dragover"].forEach((name) => elements.dropZone.addEventListener(name, (event) => { event.preventDefault(); elements.dropZone.classList.add("is-dragging"); }));
["dragleave", "drop"].forEach((name) => elements.dropZone.addEventListener(name, (event) => { event.preventDefault(); elements.dropZone.classList.remove("is-dragging"); }));
elements.dropZone.addEventListener("drop", (event) => {
  const file = event.dataTransfer.files[0];
  if (!file) return;
  const transfer = new DataTransfer();
  transfer.items.add(file);
  elements.fileInput.files = transfer.files;
  setSelectedFile(file);
});
elements.refreshButton.addEventListener("click", refreshJobs);
elements.retryButton.addEventListener("click", retrySelected);
elements.loginButton.addEventListener("click", doLogin);
elements.loginPass.addEventListener("keydown", (event) => { if (event.key === "Enter") doLogin(); });
$$(".artifact-tab").forEach((tab) => tab.addEventListener("click", () => loadArtifact(tab.dataset.artifact)));
elements.askInput.addEventListener("input", () => {
  elements.askButton.disabled = state.asking || !elements.askInput.value.trim();
});
elements.askForm.addEventListener("submit", (event) => {
  event.preventDefault();
  askQuestion(elements.askInput.value.trim());
});

refreshJobs();
detectAskMode();
setInterval(() => {
  const hasActiveJob = state.jobs.some((job) => job.status === "queued" || job.status === "processing");
  if (!document.hidden && hasActiveJob) refreshJobs();
}, 1200);
setInterval(() => { if (!document.hidden) refreshJobs(); }, 8000);
