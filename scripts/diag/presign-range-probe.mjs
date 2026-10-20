// 预签名 GET 的分段读取探针：实测「存储后端认不认 Range」。
//
// 为什么要这个脚本：`internal/cfmount` 的按需取数和 `internal/spacedav` 的
// `Seek` 都建立在「预签名 URL 只签 bucket+key，客户端可自由加 Range 头」之上。
// 这条前提是**量出来的**而不是推出来的（SigV4 未列出的头不参与校验），
// 换存储后端（RustFS → 公司 MinIO/S3 网关）时必须重测一遍。
//
// 用法：node scripts/diag/presign-range-probe.mjs [控制面地址] [clientid] [文件大小MiB]
// 前置：deploy/ruoyi-db 的 PG+Redis 与 RustFS 已起，控制面已跑新包。
// 退出码：0=后端支持 Range；1=不支持（会退回整份读，语义仍对但慢）；2=执行失败。
const base = process.argv[2] || 'http://127.0.0.1:8090';
const clientId = process.argv[3] || 'e5cd7e4891bf95d1d19206ce24a7b32e';
const sizeMiB = Number(process.argv[4] || 3);

let token = '';
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

try {
  token = (await call('POST', '/auth/login', {
    clientId, grantType: 'password', username: 'admin', password: 'admin123',
  })).access_token;

  const path = 'rangeprobe/big.bin';
  const payload = Buffer.alloc(sizeMiB * 1024 * 1024, 'x');
  const put = await call('POST', '/easyshare/drive/presign-put', { path, space: 'personal' });
  const up = await fetch(put.url, { method: 'PUT', body: payload });
  if (!up.ok) throw new Error(`上传失败 HTTP ${up.status}`);
  console.log(`已上传 ${payload.length} 字节 → ${path}`);

  const get = await call('POST', '/easyshare/drive/presign-get', { path, space: 'personal' });
  let supported = true;
  for (const range of ['bytes=1000000-1000010', 'bytes=0-99', `bytes=${payload.length - 5}-${payload.length - 1}`]) {
    const res = await fetch(get.url, { headers: { Range: range } });
    const buf = Buffer.from(await res.arrayBuffer());
    const want = Number(range.slice(6).split('-')[1]) - Number(range.slice(6).split('-')[0]) + 1;
    const ok206 = res.status === 206 && buf.length === want;
    supported = supported && ok206;
    console.log(`${ok206 ? 'OK  ' : 'NO  '} Range ${range} → HTTP ${res.status} `
      + `收到 ${buf.length}/${want} 字节 content-range=${res.headers.get('content-range')} `
      + `accept-ranges=${res.headers.get('accept-ranges')}`);
  }

  // 探针数据自清：不留残留对象与回收站行
  await call('DELETE', '/easyshare/drive/object', { path, space: 'personal' });
  for (const rec of await call('GET', '/easyshare/drive/recycle?space=personal')) {
    if (rec.path === path) await call('POST', '/easyshare/drive/recycle/purge', { fileId: rec.fileId, space: 'personal' });
  }
  const rest = (await call('GET', '/easyshare/drive/objects?space=personal')).filter(o => o.path.startsWith('rangeprobe'));
  console.log('探针数据残留：', rest.length === 0 ? '无' : rest.map(o => o.path).join(' | '));
  console.log(supported ? '\n结论：后端支持 Range，按需取数只下被读区间' : '\n结论：后端不支持 Range，将退回整份读');
  process.exit(supported ? 0 : 1);
} catch (err) {
  console.error('执行失败：', err.message);
  process.exit(2);
}
