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
    renderDiff(data.cleaning_actions);
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

function renderDiff(actions) {
  const container = $("#sub-diff");
  if (!actions || actions.length === 0) {
    container.innerHTML = '<div class="empty-state">无清洗动作（本次处理未命中规则）</div>';
    return;
  }

  // 按规则分组统计
  const byRule = {};
  for (const a of actions) {
    const key = a.rule_name || a.rule_id || "unknown";
    byRule[key] = (byRule[key] || 0) + 1;
  }
  const summary = Object.entries(byRule)
    .map(([name, count]) => `<span class="diff-rule-chip">${escapeHtml(name)} × ${count}</span>`)
    .join("");

  // 去除动作中的重复文本（同规则同文本多次命中只展示一次）
  const seen = new Set();
  const unique = actions.filter((a) => {
    const key = `${a.rule_id || ""}|${a.kind}|${a.before}`;
    if (seen.has(key)) return false;
    seen.add(key);
    return true;
  });

  const MAX_ITEMS = 100;
  const shown = unique.slice(0, MAX_ITEMS);
  const hidden = unique.length - shown.length;

  container.innerHTML = `
    <div class="diff-summary">清洗动作 ${actions.length} 条 · ${summary}</div>
    ${shown
      .map(
        (a) => `
    <div class="diff-item ${a.kind === "remove_block" ? "diff-item-removed" : ""}">
      <div class="diff-meta">
        <span class="diff-rule">${escapeHtml(a.rule_name || a.rule_id || "未知规则")}</span>
        <span class="diff-kind">${a.kind === "remove_block" ? "整块删除" : "文本改写"}</span>
        <span class="diff-block">块 ${escapeHtml(a.block_id || "—")}</span>
      </div>
      <div class="diff-before">${escapeHtml(a.before)}</div>
      ${a.kind !== "remove_block" && a.after ? `<div class="diff-after">→ ${escapeHtml(a.after)}</div>` : ""}
    </div>`
      )
      .join("")}
    ${hidden > 0 ? `<div class="diff-hidden">… 另有 ${hidden} 条同类动作未展开</div>` : ""}
  `;
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
      body: JSON.stringify({ question, top_k: topK, strategies: ["vector", "bm25", "hybrid", "reranked"] }),
    });
    const data = await resp.json();
    renderMultiStrategy(data, question);
  } catch (err) {
    $("#retrieve-results").innerHTML = `<div class="empty-state">检索失败: ${err.message}</div>`;
  } finally {
    $("#btn-search").classList.remove("loading");
  }
}

function renderMultiStrategy(data, question) {
  const container = $("#retrieve-results");
  const strategies = data.strategies || {};
  const keys = Object.keys(strategies);
  if (keys.length === 0) {
    container.innerHTML = '<div class="empty-state">未检索到结果</div>';
    return;
  }
  const keywords = extractKeywords(question);

  container.innerHTML = `
    <div style="font-size:12px;color:var(--muted);margin-bottom:12px">
      Embedder: ${data.embedder} · 问题: "${escapeHtml(question)}"
    </div>
    <div class="strategy-columns">
      ${keys.map((key) => {
        const s = strategies[key];
        return `
        <div class="strategy-col">
          <div class="strategy-header">${s.label} <span class="strategy-count">${s.result_count} 条</span></div>
          <div class="strategy-results">
            ${s.results.map((r) => `
              <div class="result-card compact">
                <div class="result-header">
                  <span class="result-rank">#${r.rank}</span>
                  <span class="result-score">${r.score}</span>
                </div>
                <div class="result-source">${escapeHtml(r.filename)} · ${formatLocations(r.source_locations)}</div>
                <div class="result-text">${highlightKeywords(escapeHtml(r.text.slice(0, 150)), keywords)}${r.text.length > 150 ? "…" : ""}</div>
              </div>
            `).join("")}
            ${s.results.length === 0 ? '<div class="empty-state" style="padding:20px">无结果</div>' : ""}
          </div>
        </div>`;
      }).join("")}
    </div>
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
// Tab 4: 健康度仪表盘
// ---------------------------------------------------------------------------
async function loadHealth() {
  const container = $("#health-content");
  container.innerHTML = '<div class="empty-state">加载中…</div>';
  try {
    const resp = await fetch("/debug/health");
    const data = await resp.json();
    renderHealth(data);
  } catch (err) {
    container.innerHTML = `<div class="empty-state">加载失败: ${err.message}</div>`;
  }
}

function renderHealth(data) {
  const container = $("#health-content");
  const scale = data.scale || {};
  const usage = data.usage || {};
  const blind = data.blind_spots || {};
  const coverage = data.coverage || {};
  const freshness = data.freshness || {};
  const gen = usage.generation || {};

  container.innerHTML = `
    <div class="stat-grid" style="margin-bottom:20px">
      <div class="stat-card"><div class="stat-value">${scale.total_documents || 0}</div><div class="stat-label">文档总数</div></div>
      <div class="stat-card"><div class="stat-value">${scale.total_chunks || 0}</div><div class="stat-label">切块总数</div></div>
      <div class="stat-card"><div class="stat-value">${scale.avg_chunks_per_doc || 0}</div><div class="stat-label">平均块/文档</div></div>
      <div class="stat-card"><div class="stat-value">${usage.total_queries || 0}</div><div class="stat-label">总查询数</div></div>
      <div class="stat-card"><div class="stat-value">${usage.recent_queries_30d || 0}</div><div class="stat-label">近30天查询</div></div>
      <div class="stat-card"><div class="stat-value">${gen.avg_faithfulness != null ? gen.avg_faithfulness : "—"}</div><div class="stat-label">平均忠实度</div></div>
    </div>

    <div class="health-sections">
      <div class="health-section">
        <h3>文档命中排行</h3>
        ${(usage.most_cited_docs || []).length === 0 ? '<div class="empty-state" style="padding:12px">暂无查询记录</div>' :
          (usage.most_cited_docs || []).map(d => `<div class="health-row"><span>${escapeHtml(d.file_id)}</span><span class="health-badge">${d.count} 次</span></div>`).join("")}
      </div>
      <div class="health-section">
        <h3>从未命中文档（僵尸文档）</h3>
        ${(usage.never_cited_docs || []).length === 0 ? '<div class="empty-state" style="padding:12px">全部文档均有命中</div>' :
          (usage.never_cited_docs || []).map(d => `<div class="health-row"><span>${escapeHtml(d)}</span></div>`).join("")}
      </div>
      <div class="health-section">
        <h3>盲区（零结果/低分查询）</h3>
        ${blind.count === 0 ? '<div class="empty-state" style="padding:12px">暂无盲区</div>' :
          (blind.unanswered_queries || []).slice(0, 10).map(q => `<div class="health-row"><span>${escapeHtml(q.question)}</span><span class="health-badge warn">${q.result_count} 条</span></div>`).join("")}
      </div>
      <div class="health-section">
        <h3>文件格式覆盖</h3>
        ${Object.entries(coverage.by_extension || {}).map(([ext, cnt]) => `<div class="health-row"><span>.${ext}</span><span class="health-badge">${cnt} 份</span></div>`).join("")}
      </div>
    </div>
  `;
}

$("#btn-health").addEventListener("click", loadHealth);

// ---------------------------------------------------------------------------
// 初始化
// ---------------------------------------------------------------------------
loadDocuments();
