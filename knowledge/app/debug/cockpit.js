/* 知识质量驾驶舱前端逻辑 */
"use strict";

const $ = (sel) => document.querySelector(sel);
const $$ = (sel) => document.querySelectorAll(sel);

// ---------------------------------------------------------------------------
// Tab 切换
// ---------------------------------------------------------------------------
$$(".tab").forEach((tab) => {
  tab.addEventListener("click", () => {
    $$(".tab").forEach((t) => t.classList.remove("active"));
    $$(".panel").forEach((p) => p.classList.remove("active"));
    tab.classList.add("active");
    $(`#tab-${tab.dataset.tab}`).classList.add("active");
  });
});

// Sub-tab 切换
$$(".sub-tab").forEach((tab) => {
  tab.addEventListener("click", () => {
    $$(".sub-tab").forEach((t) => t.classList.remove("active"));
    $$(".sub-panel").forEach((p) => p.classList.remove("active"));
    tab.classList.add("active");
    $(`#sub-${tab.dataset.sub}`).classList.add("active");
  });
});

// ---------------------------------------------------------------------------
// Tab 1: 单文档透视
// ---------------------------------------------------------------------------
async function loadDocuments() {
  const resp = await fetch("/debug/documents");
  const data = await resp.json();
  const select = $("#doc-select");
  select.innerHTML = '<option value="">选择已入库文档…</option>';
  for (const doc of data.documents) {
    const opt = document.createElement("option");
    opt.value = doc.file_id;
    opt.textContent = `${doc.filename} (${doc.file_id})`;
    select.appendChild(opt);
  }
}

async function inspectDocument(fileId) {
  if (!fileId) return;
  $("#inspect-content").style.display = "grid";
  $("#inspect-empty").style.display = "none";
  $("#inspect-content").classList.add("loading");

  try {
    const resp = await fetch(`/debug/document/${fileId}`);
    const data = await resp.json();
    renderCleanMarkdown(data.clean_markdown);
    renderChunks(data.chunks);
    renderBlocks(data.document);
    renderStats(data.stats, data.manifest);
  } catch (err) {
    console.error("inspect failed:", err);
  } finally {
    $("#inspect-content").classList.remove("loading");
  }
}

function renderCleanMarkdown(md) {
  $("#clean-md").textContent = md || "（无内容）";
}

function renderChunks(chunks) {
  const container = $("#sub-chunks");
  if (!chunks || chunks.length === 0) {
    container.innerHTML = '<div class="empty-state">无切块数据</div>';
    return;
  }
  container.innerHTML = chunks
    .map(
      (c, i) => `
    <div class="chunk-card" data-idx="${i}">
      <div class="chunk-meta">
        #${i + 1} · ${c.char_count} 字符 · 块 ${c.block_ids.join(", ") || "—"}
        · 来源 ${formatLocations(c.source_locations)}
        · ${c.extraction_methods.join("/")}
      </div>
      <div class="chunk-text">${escapeHtml(c.text.slice(0, 200))}${c.text.length > 200 ? "…" : ""}</div>
    </div>`
    )
    .join("");
}

function renderBlocks(document) {
  const container = $("#sub-blocks");
  if (!document || !document.blocks) {
    container.innerHTML = '<div class="empty-state">无结构化块数据</div>';
    return;
  }
  container.innerHTML = document.blocks
    .map((b) => {
      const cls = b.type === "heading" ? "heading" : b.type === "table" ? "table" : "";
      const label = b.type === "heading" ? `H${b.level || "?"}` : b.type;
      const preview = b.text
        ? b.text.slice(0, 80)
        : b.rows
          ? `[表格 ${b.rows.length}行×${(b.rows[0] || []).length}列]`
          : "";
      return `
      <div class="block-item ${cls}">
        <span class="block-type">${label}</span>
        ${escapeHtml(preview)}${(b.text || "").length > 80 ? "…" : ""}
      </div>`;
    })
    .join("");
}

function renderStats(stats, manifest) {
  const container = $("#sub-stats");
  const items = [
    { label: "结构化块", value: stats.blocks || 0 },
    { label: "字符数", value: stats.characters || 0 },
    { label: "切块数", value: stats.chunks || 0 },
  ];
  if (stats.ocr) {
    items.push({ label: "OCR 页", value: stats.ocr.ocr_pages || 0 });
  }
  if (stats.cleaning && stats.cleaning.hits) {
    const totalHits = Object.values(stats.cleaning.hits).reduce((a, b) => a + b, 0);
    items.push({ label: "清洗命中", value: totalHits });
  }
  container.innerHTML = `
    <div class="stat-grid">
      ${items.map((s) => `<div class="stat-card"><div class="stat-value">${s.value}</div><div class="stat-label">${s.label}</div></div>`).join("")}
    </div>
    ${manifest ? `<h3 style="margin-top:16px;font-size:12px;color:var(--muted)">Manifest</h3><pre class="code-block small">${escapeHtml(JSON.stringify(manifest, null, 2))}</pre>` : ""}
  `;
}

$("#btn-refresh-docs").addEventListener("click", loadDocuments);
$("#doc-select").addEventListener("change", (e) => inspectDocument(e.target.value));

// ---------------------------------------------------------------------------
// Tab 2: 检索调试
// ---------------------------------------------------------------------------
async function doSearch() {
  const question = $("#query-input").value.trim();
  if (!question) return;
  const topK = parseInt($("#query-topk").value) || 5;

  $("#retrieve-results").innerHTML = "";
  $("#retrieve-empty").style.display = "none";
  $("#btn-search").classList.add("loading");

  try {
    const resp = await fetch("/debug/query", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ question, top_k: topK }),
    });
    const data = await resp.json();
    renderSearchResults(data, question);
  } catch (err) {
    $("#retrieve-results").innerHTML = `<div class="empty-state">检索失败: ${err.message}</div>`;
  } finally {
    $("#btn-search").classList.remove("loading");
  }
}

function renderSearchResults(data, question) {
  const container = $("#retrieve-results");
  if (!data.results || data.results.length === 0) {
    container.innerHTML = '<div class="empty-state">未检索到结果</div>';
    return;
  }
  const keywords = extractKeywords(question);
  container.innerHTML = `
    <div style="font-size:12px;color:var(--muted);margin-bottom:8px">
      策略: ${data.strategy} · Embedder: ${data.embedder} · 命中 ${data.result_count} 条
    </div>
    ${data.results
      .map(
        (r) => `
      <div class="result-card">
        <div class="result-header">
          <span class="result-rank">#${r.rank}</span>
          <span class="result-score">score: ${r.score}</span>
        </div>
        <div class="result-source">
          ${escapeHtml(r.filename)} · ${r.file_id} · ${r.char_count} 字符
          · 来源 ${formatLocations(r.source_locations)}
        </div>
        <div class="result-text">${highlightKeywords(escapeHtml(r.text), keywords)}</div>
      </div>`
      )
      .join("")}
  `;
}

$("#btn-search").addEventListener("click", doSearch);
$("#query-input").addEventListener("keydown", (e) => { if (e.key === "Enter") doSearch(); });

// ---------------------------------------------------------------------------
// Tab 3: 生成审计
// ---------------------------------------------------------------------------
async function doGenerate() {
  const question = $("#gen-input").value.trim();
  if (!question) return;
  const topK = parseInt($("#gen-topk").value) || 5;

  $("#gen-content").style.display = "none";
  $("#gen-empty").style.display = "none";
  $("#btn-generate").classList.add("loading");

  try {
    const resp = await fetch("/debug/generate", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ question, top_k: topK }),
    });
    const data = await resp.json();
    renderGeneration(data);
  } catch (err) {
    $("#gen-empty").style.display = "block";
    $("#gen-empty").textContent = `生成失败: ${err.message}`;
  } finally {
    $("#btn-generate").classList.remove("loading");
  }
}

function renderGeneration(data) {
  $("#gen-content").style.display = "block";

  // Prompt
  if (data.prompt) {
    $("#gen-prompt").textContent = `[System]\n${data.prompt.system}\n\n[User]\n${data.prompt.user}`;
  } else {
    $("#gen-prompt").textContent = "（无 prompt）";
  }

  // Answer
  $("#gen-answer").innerHTML = data.answer
    ? escapeHtml(data.answer).replace(/\[(\d+)\]/g, '<sup style="color:var(--blue)">[$1]</sup>')
    : '<span style="color:var(--muted)">（无回答）</span>';

  // Faithfulness
  const faithContainer = $("#gen-faith");
  if (!data.faithfulness || data.faithfulness.length === 0) {
    faithContainer.innerHTML = data.llm_configured
      ? '<div class="empty-state">无法分析</div>'
      : '<div class="empty-state">未配置 LLM，无法生成回答</div>';
  } else {
    const labels = { supported: "✅ 有依据", partial: "⚠️ 部分", unsupported: "❌ 无依据" };
    faithContainer.innerHTML = data.faithfulness
      .map(
        (f) => `
      <div class="faith-item ${f.verdict}">
        <span class="faith-badge">${labels[f.verdict] || f.verdict}</span>
        ${escapeHtml(f.sentence)}
        <span style="float:right;font-size:10px;color:var(--muted)">${f.evidence_score}</span>
      </div>`
      )
      .join("");

    // 汇总
    const total = data.faithfulness.length;
    const unsupported = data.faithfulness.filter((f) => f.verdict === "unsupported").length;
    if (unsupported / total > 0.3) {
      faithContainer.innerHTML += `<div style="margin-top:8px;padding:8px;background:var(--red-bg);border-radius:6px;font-size:12px;color:var(--red)">⚠️ ${unsupported}/${total} 句无依据（${Math.round(unsupported / total * 100)}%），检索 context 可能不充分</div>`;
    }
  }

  // Contexts
  const ctxContainer = $("#gen-contexts-list");
  ctxContainer.innerHTML = (data.contexts || [])
    .map(
      (c) => `
    <div class="context-item">
      <div class="ctx-header">
        <span>[${c.index}] ${escapeHtml(c.filename)} · ${c.file_id}</span>
        <span>score: ${c.score}</span>
      </div>
      <div class="ctx-text">${escapeHtml(c.text.slice(0, 150))}${c.text.length > 150 ? "…" : ""}</div>
    </div>`
    )
    .join("");
}

$("#btn-generate").addEventListener("click", doGenerate);
$("#gen-input").addEventListener("keydown", (e) => { if (e.key === "Enter") doGenerate(); });

// ---------------------------------------------------------------------------
// 工具函数
// ---------------------------------------------------------------------------
function escapeHtml(text) {
  const div = document.createElement("div");
  div.textContent = text;
  return div.innerHTML;
}

function formatLocations(locations) {
  if (!locations || locations.length === 0) return "—";
  return locations
    .map((loc) => {
      const parts = [];
      if (loc.page != null) parts.push(`p${loc.page}`);
      if (loc.sheet) parts.push(loc.sheet);
      if (loc.slide != null) parts.push(`s${loc.slide}`);
      return parts.join("/") || JSON.stringify(loc);
    })
    .join(", ");
}

function extractKeywords(question) {
  const words = new Set();
  // 中文 2+ 字词
  (question.match(/[\u4e00-\u9fff]{2,}/g) || []).forEach((w) => words.add(w));
  // 英文词
  (question.match(/[a-zA-Z_]{2,}/g) || []).forEach((w) => words.add(w.toLowerCase()));
  // 数字
  (question.match(/\d+(?:\.\d+)?/g) || []).forEach((w) => words.add(w));
  return [...words];
}

function highlightKeywords(html, keywords) {
  let result = html;
  for (const kw of keywords) {
    const escaped = kw.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
    result = result.replace(new RegExp(escaped, "gi"), (m) => `<mark>${m}</mark>`);
  }
  return result;
}

// ---------------------------------------------------------------------------
// 初始化
// ---------------------------------------------------------------------------
loadDocuments();
