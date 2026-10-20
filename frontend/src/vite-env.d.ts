/// <reference types="vite/client" />

declare module '*.vue' {
    import type {DefineComponent} from 'vue'
    const component: DefineComponent<{}, {}, any>
    export default component
}

// 设计 token 守卫测试要读 style.css 原文。
// 本仓没装 @types/node（vitest 的 CSS 在测试里被 stub 成空串，`?raw` 也读不到内容），
// 所以只声明**测试真正用到的那几个** API，不为一个测试拉一个依赖。
declare module 'node:fs' {
    export function readFileSync(path: string, encoding: string): string
    export function existsSync(path: string): boolean
}
declare module 'node:path' {
    export function resolve(...parts: string[]): string
    export function dirname(path: string): string
}
declare const process: { cwd(): string }
