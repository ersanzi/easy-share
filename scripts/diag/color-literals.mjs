// 设计 token 纪律检查器：AGENTS 要求「组件里不写字面色值」，这条纪律必须能被机器复算，
// 否则每次「已收口」都是一句自述。
//
// 用法：
//   node scripts/diag/color-literals.mjs            # 报告：可换 / 换不了 两类计数与清单
//   node scripts/diag/color-literals.mjs --apply    # 只替换「与既有 token 值完全相等且角色匹配」的那些
//
// 为什么只自动替换「完全相等 + 角色匹配」：替换后计算值逐位不变，
// **零视觉差可以由字符串相等直接证明**，不需要人眼看。
// 对不上 token 的字面值一律只列清单不动手——那属于设计决策，不是机械整理。
//
// 规则本身以前端为真相源：frontend/src/utils/designTokensLint.ts，
// 由 frontend/src/__tests__/designTokens.test.ts 在 CI 里挡住回退。
// 本脚本只负责「改文件」（--apply / --apply-neutral / --apply-accent）与出诊断报告。
import fs from 'node:fs';

const FILE = 'frontend/src/style.css';
const apply = process.argv.includes('--apply');
const applyNeutral = process.argv.includes('--apply-neutral');
// 允许的最大明度差：默认 2（几乎只改色相），放宽到 9 才能并掉 iOS 系统灰那一族
const maxDL = Number(process.argv.find(a => a.startsWith('--max-L='))?.split('=')[1] ?? 2);
const src = fs.readFileSync(FILE, 'utf8');
const lines = src.split('\n');

// ── 1. 读 :root 里的 token 定义，并把 var() 链解开 ──────────────
const rootStart = lines.findIndex(l => l.startsWith(':root {'));
if (rootStart < 0) throw new Error('找不到 :root 块');
let rootEnd = rootStart;
while (rootEnd < lines.length && lines[rootEnd].trim() !== '}') rootEnd++;

const raw = new Map();          // name → 原始值
for (let i = rootStart; i <= rootEnd; i++) {
  const m = lines[i].match(/^\s*(--[a-z0-9-]+):\s*([^;]+);/i);
  if (m) raw.set(m[1], m[2].trim());
}
const resolved = new Map();     // name → 十六进制小写（解不开 var 链的为 null）
function resolve(name, seen = new Set()) {
  if (resolved.has(name)) return resolved.get(name);
  const value = raw.get(name);
  if (value === undefined || seen.has(name)) return null;
  seen.add(name);
  const hex = value.match(/^#([0-9a-fA-F]{3}|[0-9a-fA-F]{6})$/);
  if (hex) {
    const norm = hex[1].length === 3 ? hex[1].split('').map(c => c + c).join('') : hex[1];
    resolved.set(name, '#' + norm.toLowerCase());
    return resolved.get(name);
  }
  const ref = value.match(/^var\((--[a-z0-9-]+)\)$/);
  const out = ref ? resolve(ref[1], seen) : null;
  resolved.set(name, out);
  return out;
}
for (const name of raw.keys()) resolve(name);

// 角色 → 允许的 token 前缀/名单。宁可漏换，不可错换。
const ROLE = {
  text: ['--text', '--ink', '--muted', '--faint', '--blue', '--green', '--red', '--orange',
    '--purple', '--amber', '--cyan', '--blue-dark', '--accent'],
  surface: ['--bg', '--bg-panel', '--bg-chrome', '--blue-selected', '--blue-light',
    '--green', '--red', '--orange', '--blue', '--amber', '--cyan', '--purple'],
  line: ['--border-strong', '--kd-text-6', '--blue-2', '--blue-3'],
};
const byRole = {};
for (const [role, names] of Object.entries(ROLE)) {
  byRole[role] = new Map();
  for (const n of names) {
    const v = resolve(n);
    if (!v) continue;
    // 同一角色内值重复时，保留**声明顺序靠前**的语义名（更贴近设计体系的说法）
    if (!byRole[role].has(v)) byRole[role].set(v, n);
  }
}

// ── 2. 扫 :root 之外的字面色值 ────────────────────────────────
const hexRe = /#([0-9a-fA-F]{6}|[0-9a-fA-F]{3})\b/g;
const stats = { total: 0, replaceable: 0, kept: 0 };
const kept = [];
const out = lines.slice();

for (let i = rootEnd + 1; i < lines.length; i++) {
  const line = lines[i];
  if (!hexRe.test(line)) { hexRe.lastIndex = 0; continue; }
  hexRe.lastIndex = 0;
  // 判断这一行的角色：看属性名。多属性行（一行式规则）按「先出现的主导属性」处理，
  // 认不出来就不动——保守优先。
  const props = [...line.matchAll(/([a-z-]+)\s*:\s*[^;{]*/gi)].map(m => m[1].toLowerCase());
  let role = null;
  if (props.some(p => p === 'color')) role = 'text';
  else if (props.some(p => p === 'background' || p === 'background-color')) role = 'surface';
  else if (props.some(p => /^(border|border-\w+|border-top|border-bottom|border-left|border-right|outline)$/.test(p))) role = 'line';
  if (!role) {
    for (const m of line.matchAll(hexRe)) { stats.total++; stats.kept++; kept.push(`  ${i + 1}: 属性角色不明，保留  ${m[0]}`); }
    continue;
  }
  let newLine = line;
  for (const m of line.matchAll(hexRe)) {
    stats.total++;
    const norm = m[1].length === 3 ? m[1].split('').map(c => c + c).join('') : m[1];
    const key = '#' + norm.toLowerCase();
    const token = byRole[role].get(key);
    if (!token) {
      stats.kept++;
      kept.push(`  ${i + 1}: ${role} 角色下没有值相等的 token  ${key}  |  ${line.trim().slice(0, 90)}`);
      continue;
    }
    stats.replaceable++;
    if (apply) newLine = newLine.split(m[0]).join(`var(${token})`);
  }
  if (apply) out[i] = newLine;
}

console.log(`${apply ? '已替换' : '可替换'} ${stats.replaceable} 处 / 共 ${stats.total} 处字面色值；保留 ${stats.kept} 处（对不上 token 或角色不明）`);

// ── 3. 近似重复聚类：同一个语义角色下到底有几种写法 ──────────────
//
// 这一步才是重点。机械替换只能处理「值完全相等」的少数，
// 真实情况是组件各自发明了「绿字」「红字」「紫底」的深浅值——
// 这正是 KD 三层体系要防的事（见 wps-dingtalk-ui-reverse.md §2.4「状态是相邻阶」）。
function hueSatLight(hex) {
  const r = parseInt(hex.slice(1, 3), 16) / 255;
  const g = parseInt(hex.slice(3, 5), 16) / 255;
  const b = parseInt(hex.slice(5, 7), 16) / 255;
  const max = Math.max(r, g, b), min = Math.min(r, g, b), d = max - min;
  let h = 0;
  if (d !== 0) {
    if (max === r) h = ((g - b) / d) % 6;
    else if (max === g) h = (b - r) / d + 2;
    else h = (r - g) / d + 4;
    h = Math.round(h * 60);
    if (h < 0) h += 360;
  }
  const l = (max + min) / 2;
  const s = d === 0 ? 0 : d / (1 - Math.abs(2 * l - 1));
  return { h, s: Math.round(s * 100), l: Math.round(l * 100) };
}
const seen = new Map();   // 规范值 → 出现次数
for (let i = rootEnd + 1; i < lines.length; i++) {
  for (const m of lines[i].matchAll(hexRe)) {
    const norm = m[1].length === 3 ? m[1].split('').map(c => c + c).join('') : m[1];
    const key = '#' + norm.toLowerCase();
    seen.set(key, (seen.get(key) || 0) + 1);
  }
}
const accents = ['--green', '--red', '--orange', '--purple', '--amber', '--cyan', '--blue'];
console.log('\n近似重复聚类（与语义色的色相差 ≤ 24° 且饱和度 ≥ 15%）：');
for (const token of accents) {
  const tv = resolve(token);
  if (!tv) continue;
  const t = hueSatLight(tv);
  const near = [...seen.entries()]
    .filter(([hex]) => {
      const c = hueSatLight(hex);
      if (c.s < 15) return false;
      let dh = Math.abs(c.h - t.h);
      if (dh > 180) dh = 360 - dh;
      return dh <= 24;
    })
    .sort((a, b) => b[1] - a[1]);
  const others = near.filter(([hex]) => hex !== tv);
  console.log(`  ${token.padEnd(9)} ${tv}  同色系字面值 ${near.length} 种（不含自身 ${others.length} 种），共出现 ${near.reduce((n, [, c]) => n + c, 0)} 次`);
  for (const [hex, count] of others.slice(0, 8)) {
    const c = hueSatLight(hex);
    console.log(`      ${hex} ×${count}  (H${c.h} S${c.s}% L${c.l}%)`);
  }
}
const neutrals = [...seen.entries()].filter(([hex]) => hueSatLight(hex).s < 15)
  .sort((a, b) => b[1] - a[1]);
console.log(`\n中性色（S<15%）字面值 ${neutrals.length} 种，共出现 ${neutrals.reduce((n, [, c]) => n + c, 0)} 次；前 12 种：`);
console.log('  ' + neutrals.slice(0, 12).map(([h, c]) => `${h}×${c}`).join('  '));

if (kept.length) {
  console.log('\n保留清单（需要设计决策，不是机械整理）：');
  console.log(kept.slice(0, 60).join('\n'));
  if (kept.length > 60) console.log(`  …另有 ${kept.length - 60} 条`);
}
if (apply) {
  fs.writeFileSync(FILE, out.join('\n'));
  console.log('\n已写回 ' + FILE);
}

// ── 4. 中性色归并（--apply-neutral）───────────────────────────
//
// 目标不是「少写几个色值」，而是修 §1 规律2 的违规：中性色必须带 220° 蓝相，
// 纯灰/系统灰（iOS 的 #c7c7cc、#e9e9ed、#77777c 那一族）在浅底上会发死。
//
// 保守边界，三条都满足才动：
//   1. 只碰近中性（S < 15%），且**不碰纯白**（白既可能是底面也可能是字色，语义要人判）；
//   2. 只换成**明度差 ≤ 2** 的同角色 token —— 改动只发生在色相/饱和度上，
//      明度不动，视觉位移最小；
//   3. 跳过海报/汇报卡皮肤（`--p-*` 与 1841 行之后）与深色模式块——
//      那两套是独立主题，色带另有出处。
const NEUTRAL_TARGETS = {
  text: ['--text', '--ink', '--muted', '--faint', '--kd-text-3', '--kd-text-4'],
  surface: ['--bg-panel', '--bg-chrome', '--border-strong'],
  line: ['--border-strong'],
};
const posterStart = lines.findIndex(l => l.includes('图文海报'));
const neutralChanges = [];
for (let i = rootEnd + 1; i < (posterStart > 0 ? posterStart : lines.length); i++) {
  const line = lines[i];
  if (line.includes('--p-') || /prefers-color-scheme/.test(line)) continue;
  const props = [...line.matchAll(/([a-z-]+)\s*:\s*[^;{]*/gi)].map(m => m[1].toLowerCase());
  let role = null;
  if (props.includes('color')) role = 'text';
  else if (props.includes('background') || props.includes('background-color')) role = 'surface';
  else if (props.some(p => /^(border|border-\w+|outline)$/.test(p))) role = 'line';
  if (!role) continue;
  let newLine = line;
  for (const m of line.matchAll(hexRe)) {
    const norm = m[1].length === 3 ? m[1].split('').map(c => c + c).join('') : m[1];
    const key = '#' + norm.toLowerCase();
    if (key === '#ffffff' || key === '#fff') continue;
    const c = hueSatLight(key);
    if (c.s >= 15) continue;
    let best = null;
    for (const token of NEUTRAL_TARGETS[role]) {
      const tv = resolve(token);
      if (!tv) continue;
      const t = hueSatLight(tv);
      const dL = Math.abs(t.l - c.l);
      if (dL <= maxDL && (!best || dL < best.dL)) best = { token, dL, to: tv, toS: t.s };
    }
    if (!best || key === best.to) continue;
    neutralChanges.push({ line: i + 1, role, from: key, fromS: c.s, to: best.to, toS: best.toS, token: best.token, dL: best.dL });
    newLine = newLine.split(m[0]).join(`var(${best.token})`);
  }
  if (newLine !== line && applyNeutral) out[i] = newLine;
}
console.log(`\n中性色归并：${applyNeutral ? '已换' : '可换'} ${neutralChanges.length} 处（明度差 ≤ ${maxDL}）`);
for (const chg of neutralChanges.slice(0, 24)) {
  console.log(`  ${String(chg.line).padStart(4)} ${chg.role.padEnd(7)} ${chg.from}(S${chg.fromS}) → ${chg.token} ${chg.to}(S${chg.toS}) ΔL${chg.dL}`);
}
if (neutralChanges.length > 24) console.log(`  …另有 ${neutralChanges.length - 24} 处`);
if (applyNeutral) {
  fs.writeFileSync(FILE, out.join('\n'));
  console.log('已写回 ' + FILE);
}

// ── 5. 语义色档位归并（--apply-accent）────────────────────────
//
// 一个语义色只有一档基色时，组件要「压在浅底上的深色文字」只能自己调一个——
// 于是仓库里长出 7 种绿、7 种红、9 种琥珀。KD 的答案是相邻阶（§2.4），
// 这里落成 --x-text / --x-tint 两档，取值用**实际用得最多的那个**，
// 让多数用法原地不动，只有零散发明值向它靠齐。
// 表是手写的、逐条可审；蓝/青不并（蓝有 10 阶色阶，青的成员其实多半是蓝）。
const ACCENT_MAP = {
  '--green-text': ['#25883d', '#1c6b33', '#1f6b32', '#16834b', '#268f41', '#1f7a3d', '#1d8a43'],
  '--green-tint': ['#ecf9ef', '#eaf9ef'],
  '--red-text': ['#a7241e', '#be2d26', '#c0392b', '#c92c2b', '#a3271f', '#8f514d', '#d12e26'],
  '--red-tint': ['#fff0ef', '#fff3f2'],
  '--amber-text': ['#8a6100', '#b16b00', '#be7210', '#c8860a', '#8a6500', '#a56b00', '#986400', '#b06a00', '#b06000'],
  '--amber-tint': ['#fff5df', '#fff5e5', '#fef3dc'],
  '--purple-text': ['#6a56c9', '#7968d4', '#7461d2'],
  '--purple-tint': ['#f1efff'],
};
const accentApply = process.argv.includes('--apply-accent');
const accentChanges = [];
const posterLine = lines.findIndex(l => l.includes('图文海报'));
for (let i = rootEnd + 1; i < (posterLine > 0 ? posterLine : lines.length); i++) {
  const line = lines[i];
  if (line.trimStart().startsWith('/*') || line.includes('--p-')) continue;
  let newLine = line;
  for (const [token, values] of Object.entries(ACCENT_MAP)) {
    for (const v of values) {
      if (!newLine.toLowerCase().includes(v)) continue;
      const before = newLine;
      newLine = newLine.split(v).join(`var(${token})`);
      if (before !== newLine) accentChanges.push({ line: i + 1, from: v, token });
    }
  }
  if (newLine !== line && accentApply) out[i] = newLine;
}
console.log(`\n语义色档位归并：${accentApply ? '已换' : '可换'} ${accentChanges.length} 处`);
for (const chg of accentChanges.slice(0, 40)) console.log(`  ${String(chg.line).padStart(4)} ${chg.from} → var(${chg.token})`);
if (accentChanges.length > 40) console.log(`  …另有 ${accentChanges.length - 40} 处`);
if (accentApply && accentChanges.length) {
  fs.writeFileSync(FILE, out.join('\n'));
  console.log('已写回 ' + FILE);
}
