// 递归删除非空文件夹 —— 活栈端到端验证（对着真实控制面 + RustFS 跑，不看单测）。
// 用法：node scripts/diag/recursive-delete-check.mjs [控制面地址] [clientid]
// 前置：docker compose up -d（PG/Redis/RustFS）+ deploy/ruoyi-db/run-ruoyi-admin.ps1
const base = process.argv[2] || 'http://127.0.0.1:8090';
const clientId = process.argv[3] || 'e5cd7e4891bf95d1d19206ce24a7b32e';

let token = '';
const results = [];
const ok = (name, pass, detail = '') => {
  results.push({ name, pass, detail });
  console.log(`${pass ? 'PASS' : 'FAIL'}  ${name}${detail ? '  — ' + detail : ''}`);
};

async function call(method, path, body) {
  const res = await fetch(base + path, {
    method,
    headers: {
      'Content-Type': 'application/json',
      clientid: clientId,
      ...(token ? { Authorization: 'Bearer ' + token } : {}),
    },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const json = await res.json().catch(() => ({}));
  if (json.code !== 200) throw new Error(`${method} ${path} -> ${json.code} ${json.msg}`);
  return json.data;
}

const listDir = () => call('GET', '/easyshare/drive/objects?space=personal&includeDirs=true');
const paths = () => listDir().then(objs => objs.map(o => o.path));

async function putFile(rel, content) {
  const presign = await call('POST', '/easyshare/drive/presign-put', { path: rel, space: 'personal' });
  const res = await fetch(presign.url, { method: 'PUT', body: content });
  if (!res.ok) throw new Error(`上传 ${rel} 失败：HTTP ${res.status}`);
}

try {
  const login = await call('POST', '/auth/login', {
    clientId, grantType: 'password', username: 'admin', password: 'admin123',
  });
  token = login.access_token;
  ok('登录控制面', Boolean(token));

  // 造一棵三层子树：rectest/a.txt + rectest/sub/b.txt + 两个空目录标记
  await call('POST', '/easyshare/drive/mkdir', { path: 'rectest', space: 'personal' });
  await call('POST', '/easyshare/drive/mkdir', { path: 'rectest/sub', space: 'personal' });
  await call('POST', '/easyshare/drive/mkdir', { path: 'rectest/emptydir', space: 'personal' });
  await putFile('rectest/a.txt', 'A');
  await putFile('rectest/sub/b.txt', 'B');
  let seen = await paths();
  ok('删除前子树齐备', ['rectest/', 'rectest/sub/', 'rectest/emptydir/', 'rectest/a.txt', 'rectest/sub/b.txt']
    .every(p => seen.includes(p)), seen.filter(p => p.startsWith('rectest')).join(' | '));

  // 递归删整棵 rectest
  await call('DELETE', '/easyshare/drive/object', { path: 'rectest/', space: 'personal', recursive: true });
  seen = await paths();
  const leftover = seen.filter(p => p.startsWith('rectest'));
  ok('递归删后目录与子目录标记全部消失', leftover.length === 0, leftover.join(' | ') || '无残留');

  // 文件应进回收站且带原路径快照（对象此刻还在，还原要用）
  const recycled = await call('GET', '/easyshare/drive/recycle?space=personal');
  const mine = recycled.filter(r => (r.originPath || r.path).startsWith('rectest/'));
  ok('两个子文件都进了回收站', mine.length === 2, mine.map(r => r.path).join(' | '));

  // 还原其中一个 → 文件回来，且目录按路径重新出现（对象没被删才有内容）
  const target = mine.find(r => r.path === 'rectest/a.txt');
  await call('POST', '/easyshare/drive/recycle/restore', { fileId: target.fileId, space: 'personal' });
  seen = await paths();
  ok('还原后文件回到原路径', seen.includes('rectest/a.txt'));
  const presign = await call('POST', '/easyshare/drive/presign-get', { path: 'rectest/a.txt', space: 'personal' });
  const got = await fetch(presign.url).then(r => r.text());
  ok('还原出来的内容仍可读', got === 'A', `读回 ${JSON.stringify(got)}`);
  ok('目录标记不复活（按路径推导，不进回收站）', !seen.includes('rectest/'));

  // 非递归删除只删目录自身：emptydir 里没有文件，行为与既有语义一致
  await call('POST', '/easyshare/drive/mkdir', { path: 'otherdir', space: 'personal' });
  await putFile('otherdir/keep.txt', 'K');
  await call('DELETE', '/easyshare/drive/object', { path: 'otherdir/', space: 'personal' });
  seen = await paths();
  ok('非递归删只摘标记、子文件不动', seen.includes('otherdir/keep.txt') && !seen.includes('otherdir/'));

  // 清理：本轮造的数据全部落地删除。
  // 注意**被还原过的文件已是 active**，不在回收站清单里，必须再删一次才清得掉
  // （第一版就是漏了这步，导致「测试数据已清干净」这条断言真红了一次——
  //  断言没错，是清理逻辑错；这类自清理漏洞会让后面的活栈用例被残留数据干扰）
  const leftoverPaths = async () => (await paths()).filter(p => p.startsWith('rectest') || p.startsWith('otherdir'));
  for (const p of await leftoverPaths()) {
    await call('DELETE', '/easyshare/drive/object', { path: p, space: 'personal' });
  }
  for (const r of await call('GET', '/easyshare/drive/recycle?space=personal')) {
    if ((r.path || '').startsWith('rectest') || (r.path || '').startsWith('otherdir')
      || (r.originPath || '').startsWith('rectest') || (r.originPath || '').startsWith('otherdir')) {
      await call('POST', '/easyshare/drive/recycle/purge', { fileId: r.fileId, space: 'personal' });
    }
  }
  const residue = await leftoverPaths();
  ok("测试数据已清干净", residue.length === 0, residue.join(" | ") || "无残留");
} catch (err) {
  ok('执行过程无异常', false, err.message);
}

const failed = results.filter(r => !r.pass).length;
console.log(`\n合计 ${results.length} 项，失败 ${failed} 项`);
process.exit(failed ? 1 : 0);
