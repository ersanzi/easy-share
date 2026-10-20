// 文档引用的可校验性：状态类文档最大的风险不是写得不好，而是**代码变了、文档没变**。
// 本仓已经为此付过学费——两份取证文档的「差距清单」过期，直接把结论带偏过两次
// （见 docs/iterations/2026-10-18-递归删除非空文件夹.md 的教训一节）。
//
// 规则：文档里凡写成 `路径` · `符号` 的证据，必须
//   1. 文件在仓库里存在；
//   2. 符号能在该文件里找到（纯文本命中即可）。
// 另外：链接方括号里不能是空串（`[](x.md)` 渲染出来是看不见的空文本）。
// 之所以用「文件 + 符号」而不是行号：行号改一次就烂，符号重命名时这个检查会立刻红。
//
// 用法：
//   node scripts/diag/check-doc-refs.mjs [markdown 文件或目录...]   # 默认递归检查 docs/
//   node scripts/diag/check-doc-refs.mjs --self-test                # 自检：注入坏引用，要求被抓到
// 加 --self-test 是因为「永不报错的闸门等于没有闸门」——本脚本有多条跳过规则
// （路由并排、散文、图片空 alt、代码片段里的例子），每条都可能把真问题静默吞掉，
// 所以每个跳过分支和一个报错分支都要有用例证明它只在应该生效时生效。
import fs from 'node:fs';
import path from 'node:path';

const repoRoot = process.cwd();

// 传目录就递归收 markdown，这样默认口径是「整个 docs/ 都归闸门管」，
// 而不是只盯某一篇文章——新写的取证文档不用记得往脚本里加。
function collectDocs(entry) {
  const abs = path.resolve(repoRoot, entry);
  if (!fs.existsSync(abs)) return [entry];          // 交给上层报「文件不存在」
  if (fs.statSync(abs).isFile()) return [entry];
  const out = [];
  for (const child of fs.readdirSync(abs, { withFileTypes: true })) {
    const rel = path.join(entry, child.name);
    if (child.isDirectory()) out.push(...collectDocs(rel));
    else if (/\.md$/i.test(child.name)) out.push(rel);
  }
  return out;
}

// `path` · `symbol`：路径必须像文件（含 / 或 .扩展名），且排除纯反引号词
const REF_RE = /`([^`]+)`\s*·\s*`([^`]+)`/g;

// 跳过规则一：接口清单里 ``/api/a` · `/api/b` 这种「两个路由并排」的写法
// （architecture.md 的 MCP 那一行就是这样）。两边都以 / 开头 = 路由枚举，不是文件引用。
const isRoutePair = (refPath, symbol) => refPath.startsWith('/') && symbol.startsWith('/');
// 跳过规则二：两边都不含斜杠、也没有扩展名 → 是「词 · 词」的散文写法，不是引用。
const looksLikeFile = (refPath) => refPath.includes('/') || /\.[a-z]+$/i.test(refPath);

// 返回一条文档里的引用清单（含行号），跳过判定集中在这里，便于自测比对。
function extractRefs(text) {
  const refs = [];
  for (const m of text.matchAll(REF_RE)) {
    const [, refPath, symbol] = m;
    if (isRoutePair(refPath, symbol)) continue;
    if (!looksLikeFile(refPath)) continue;
    refs.push({ refPath, symbol, line: text.slice(0, m.index).split('\n').length });
  }
  return refs;
}

// 逐条核对：返回失败说明数组，空数组代表全部可解析。
function verifyRefs(refs, doc) {
  const failures = [];
  for (const { refPath, symbol, line } of refs) {
    const abs = path.resolve(repoRoot, refPath);
    if (!fs.existsSync(abs)) {
      failures.push(`${doc}:${line} 文件不存在 → ${refPath}`);
      continue;
    }
    if (!fs.readFileSync(abs, 'utf8').includes(symbol)) {
      failures.push(`${doc}:${line} 符号在 ${refPath} 里找不到 → ${symbol}`);
    }
  }
  return failures;
}

// 把代码片段拉平成长度相同的空格：围栏代码块 + 行内反引号。
// 长度不变是为了行号与下标照旧对得上（引用符号那条规则反过来要求保留反引号，所以只对链接检查用）。
function blankOutCode(text) {
  return text
    .replace(/^```[\s\S]*?^```[^\n]*$/gm, (block) => block.replace(/[^\n]/g, ' '))
    .replace(/`[^`\n]*`/g, (span) => span.replace(/[^\n]/g, ' '));
}

// 链接文字被吃掉（`[](x.md)`）在渲染后是**一个看不见的空串**，人眼扫过去只会以为文档没问题。
// 本轮用 shell 改文档时就产出过两条这种链接，当时逐条核对行数与标题没发现。
// 两条豁免：图片 `![](a.png)` 的空 alt 是合法写法（要求前一字符不是 `!`）；
// 写在代码片段里的例子不会渲染成链接，所以先拉平围栏块与行内反引号——否则文档连「怎么解释这个坑」都写不了。
function scanEmptyLinkText(rawText, doc) {
  const text = blankOutCode(rawText);
  const failures = [];
  for (const m of text.matchAll(/(^|[^!])\[+\]\([^)]*\)/g)) {
    failures.push(`${doc}:${text.slice(0, m.index).split('\n').length} 链接文字为空 → ${rawText.slice(m.index, m.index + 40).trim()}`);
  }
  return failures;
}

// 一篇文档的全部核对项，返回失败说明数组（自检与正式检查共用同一条代码路径）。
function checkDocText(doc, text) {
  return [...verifyRefs(extractRefs(text), doc), ...scanEmptyLinkText(text, doc)];
}

// ── 自检：闸门本身是否会漏报 ────────────────────────────────────
if (process.argv.includes('--self-test')) {
  const real = 'scripts/diag/check-doc-refs.mjs';
  const cases = [
    { name: '文件不存在必须报错', text: '`没有这个文件.x` · `verifyRefs`', wantFail: 1 },
    { name: '符号改名后必须报错', text: '`internal/api/server.go` · `NoSuchSymbolZzz`', wantFail: 1 },
    { name: '真引用必须放过', text: `\`${real}\` · \`extractRefs\``, wantFail: 0 },
    { name: '路由并排必须跳过', text: '`/api/mcp/info` · `/api/mcp/regenerate`', wantFail: 0 },
    { name: '散文「词 · 词」必须跳过', text: '`学` · `落地`', wantFail: 0 },
    { name: '链接文字被吃掉必须报错', text: '现状一律以 [](../benchmark-absorption.md) 为准', wantFail: 1 },
    { name: '正常链接必须放过', text: '现状以 [吸收清单](../benchmark-absorption.md) 为准', wantFail: 0 },
    { name: '空 alt 的图片必须放过', text: '![截图](build/shot.png)', wantFail: 0 },
    { name: '写在反引号里的例子必须放过', text: '产出过这种空链接：`[](../x.md)`', wantFail: 0 },
    { name: '围栏代码块里的例子必须放过', text: '```md\n[](../x.md)\n```', wantFail: 0 },
  ];
  let bad = 0;
  for (const c of cases) {
    const got = checkDocText('self-test', c.text).length;
    const ok = got === c.wantFail;
    if (!ok) bad++;
    console.log(`${ok ? '✓' : '✗'} ${c.name}（期望失败 ${c.wantFail}，实得 ${got}）`);
  }
  console.log(bad ? `\n自检失败 ${bad} 组：本检查器不可信` : '\n自检通过：注入的坏引用都能被抓到，跳过规则只跳过该跳过的');
  process.exit(bad ? 1 : 0);
}

const args = process.argv.slice(2).filter(a => !a.startsWith('--'));
const docs = (args.length ? args : ['docs']).flatMap(collectDocs);

let checked = 0;
const failures = [];
for (const doc of docs) {
  const docPath = path.resolve(repoRoot, doc);
  if (!fs.existsSync(docPath)) {
    failures.push(`${doc}: 文件不存在`);
    continue;
  }
  const text = fs.readFileSync(docPath, 'utf8');
  checked += extractRefs(text).length;
  failures.push(...checkDocText(doc, text));
}

console.log(`检查 ${checked} 条 ` + '`文件 · 符号`' + ` 引用，失败 ${failures.length} 条`);
for (const f of failures) console.log('  ✗ ' + f);
if (failures.length) process.exit(1);
console.log('全部引用可解析');
