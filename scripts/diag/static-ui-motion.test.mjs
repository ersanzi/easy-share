// 静态页动效守卫：零新增依赖；node --test scripts/diag/static-ui-motion.test.mjs
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync, readdirSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import vm from 'node:vm';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
const directories = ['plugins/clipboard', 'plugins/todo', 'plugins/template',
  'knowledge/app/lab', 'knowledge/app/debug', 'knowledge/app/wps_addon'];
const sources = directories.flatMap(dir => readdirSync(path.join(root, dir))
  .filter(name => /\.(html|css|js)$/.test(name))
  .map(name => ({ file: dir + '/' + name, text: readFileSync(path.join(root, dir, name), 'utf8') })));
const styles = sources.flatMap(({file, text}) => file.endsWith('.css') ? [{file,text}]
  : [...text.matchAll(/<style[^>]*>([\s\S]*?)<\/style>/g)].map(match => ({file,text:match[1]})));
const scripts = sources.flatMap(({file,text}) => file.endsWith('.js') ? [{file,text}]
  : [...text.matchAll(/<script(?:\s[^>]*)?>([\s\S]*?)<\/script>/g)]
    .filter(match => match[1].trim()).map(match => ({file,text:match[1]})));

for (const {file,text} of styles) {
  test(file + ': token、合成属性、减少动态效果', () => {
    for (const [token,value] of Object.entries({fast:'120ms',enter:'200ms',exit:'120ms',ease:'cubic-bezier(.2,0,0,1)'})) {
      const declaration = text.match(new RegExp('--motion-' + token + '\\s*:\\s*([^;]+)'));
      assert.equal(declaration?.[1].replace(/\s/g, ''), value);
    }
    assert.match(text, /prefers-reduced-motion:\s*no-preference/);
    assert.doesNotMatch(text, /(?:animation|transition)-duration\s*:\s*0\.0*1ms/);
    for (const match of text.matchAll(/transition(?:-property)?\s*:\s*([^;{}]+)/g)) {
      assert.doesNotMatch(match[1], /\b(all|width|height|min-height|max-height|left|right|top|bottom|margin|padding|box-shadow)\b/,
        file + ': 不允许布局或全属性过渡');
    }
    assert.doesNotMatch(text, /\*[^{}]*\{[^{}]*(?:transition|animation):\s*none/,
      '不能用全局禁用抹掉状态反馈');
  });
}
for (const {file,text} of scripts) {
  test(file + ': JS 可解析且状态不依赖动画结束', () => {
    new vm.Script(text, {filename:file});
    assert.doesNotMatch(text, /addEventListener\s*\(\s*['"](?:animationend|transitionend)['"]/);
    assert.doesNotMatch(text, /(?:quotaFill|progressFill|progress-fill)[^\n]*style\.width/);
    assert.doesNotMatch(text, /\b(offsetWidth|offsetHeight)\b/, '不能用强制重排来重启动画');
    if (file.includes('wps_addon')) assert.doesNotMatch(text, /\b(?:const|let)\s+\w+|=>|`/, 'WPS 保留 ES5 降级路径');
  });
}

// 直接执行实际辅助函数，覆盖系统偏好变化、快速替换和无 WAAPI 的宿主。
for (const {file,text} of scripts.filter(source => source.text.includes('var stateMotionQuery'))) {
  const indent = text.match(/^([ \t]*)var stateMotionQuery/m)[1];
  const normalized = text.split('\n').map(line => line.startsWith(indent) ? line.slice(indent.length) : line).join('\n');
  const start = normalized.indexOf('var stateMotionQuery');
  const end = normalized.indexOf('\n}', normalized.indexOf('if (stateMotionQuery)')) + 2;
  const helper = normalized.slice(start, end);
  test(file + ': 动效打断与无动效最终态', () => {
    const animations = [];
    let listener;
    const query = {matches:true, addEventListener:(_,fn) => {listener = fn;}};
    const context = vm.createContext({window:{matchMedia:() => query}, document:{documentElement:{}},
      getComputedStyle:() => ({getPropertyValue:key => key === '--motion-enter' ? '200ms' : 'cubic-bezier(.2,0,0,1)'})});
    vm.runInContext(helper, context);
    const element = {textContent:'最终内容', animate(frames, options) {
      assert.equal(options.duration, 200);
      assert.equal(frames.at(-1).opacity, 1);
      const animation = {cancelled:false, cancel() { this.cancelled = true; this.oncancel?.(); }};
      animations.push(animation);
      return animation;
    }};
    context.revealState(element);
    context.revealState(element);
    assert.equal(animations[0].cancelled, true, '同一内容替换应取消旧动效');
    query.matches = false;
    listener();
    assert.equal(animations[1].cancelled, true, '系统减少动态效果切换应立即取消');
    context.revealState(element);
    assert.equal(animations.length, 2);
    assert.equal(element.textContent, '最终内容');
    query.matches = true;
    assert.doesNotThrow(() => context.revealState({textContent:'旧宿主也可见'}));
  });
}
