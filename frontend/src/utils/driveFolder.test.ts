import { describe, expect, it } from 'vitest'
import type { CloudFile } from '../types/core'
import { breadcrumbsFor, buildDriveView, relativeDir } from './driveFolder'

const file = (key: string, size = 100, lastModified = '2026-09-01T10:00:00Z'): CloudFile => ({
  key,
  name: key.slice(key.lastIndexOf('/') + 1),
  size,
  contentType: 'application/octet-stream',
  lastModified,
  previewKind: 'unsupported',
})

const corpus = [
  file('报告.docx', 1024, '2026-09-05T08:00:00Z'),
  file('photos/a.jpg', 10, '2026-09-01T08:00:00Z'),
  file('photos/2024/b.jpg', 20, '2026-09-02T08:00:00Z'),
  file('photos/2024/c.png', 30, '2026-09-03T08:00:00Z'),
  file('docs/说明.pdf', 40, '2026-09-04T08:00:00Z'),
  { ...file('empty/'), size: 0 }, // S3 目录占位对象
]

describe('buildDriveView', () => {
  it('根目录：直接文件 + 一级文件夹，文件夹在前', () => {
    const view = buildDriveView(corpus, { path: '' })
    expect(view.folders.map(f => f.name)).toEqual(['docs', 'empty', 'photos'])
    expect(view.files.map(f => f.name)).toEqual(['报告.docx'])
    expect(view.folderCount).toBe(3)
    expect(view.fileCount).toBe(1)
    expect(view.totalSize).toBe(1024)
  })

  it('进入文件夹：只含直接子项，占位对象不作为文件出现', () => {
    const view = buildDriveView(corpus, { path: 'photos' })
    expect(view.folders.map(f => f.path)).toEqual(['photos/2024'])
    expect(view.files.map(f => f.name)).toEqual(['a.jpg'])
    // empty/ 占位：产生文件夹，不产生文件
    const root = buildDriveView(corpus, { path: '' })
    expect(root.files.some(f => f.key === 'empty/' || f.key === 'empty')).toBe(false)
  })

  it('面包屑：根 → 逐级目录，路径可回跳', () => {
    const view = buildDriveView(corpus, { path: 'photos/2024' })
    expect(view.breadcrumbs).toEqual([
      { name: '网盘', path: '' },
      { name: 'photos', path: 'photos' },
      { name: '2024', path: 'photos/2024' },
    ])
    expect(breadcrumbsFor('')).toEqual([{ name: '网盘', path: '' }])
  })

  it('搜索：跨当前目录全部后代按名称匹配，文件夹隐藏', () => {
    const view = buildDriveView(corpus, { path: 'photos', query: '.jpg' })
    expect(view.searching).toBe(true)
    expect(view.folders).toEqual([])
    expect(view.files.map(f => f.key).sort()).toEqual(['photos/2024/b.jpg', 'photos/a.jpg'])
    expect(view.totalSize).toBe(30)
    // 目录范围之外不命中
    const scoped = buildDriveView(corpus, { path: 'docs', query: '.jpg' })
    expect(scoped.files).toEqual([])
  })

  it('排序：名称/大小/时间，方向可切换', () => {
    const byName = buildDriveView(corpus, { path: 'photos/2024', sortKey: 'name', sortDir: 'asc' })
    expect(byName.files.map(f => f.name)).toEqual(['b.jpg', 'c.png'])
    const byNameDesc = buildDriveView(corpus, { path: 'photos/2024', sortKey: 'name', sortDir: 'desc' })
    expect(byNameDesc.files.map(f => f.name)).toEqual(['c.png', 'b.jpg'])
    const bySize = buildDriveView(corpus, { path: 'photos/2024', sortKey: 'size', sortDir: 'desc' })
    expect(bySize.files.map(f => f.name)).toEqual(['c.png', 'b.jpg'])
    const byTime = buildDriveView(corpus, { path: '', sortKey: 'lastModified', sortDir: 'desc' })
    expect(byTime.files[0]?.name).toBe('报告.docx')
  })

  it('空目录与空列表有兜底视图', () => {
    const empty = buildDriveView([], { path: '' })
    expect(empty.files).toEqual([])
    expect(empty.folders).toEqual([])
    expect(empty.breadcrumbs).toEqual([{ name: '网盘', path: '' }])
    const deep = buildDriveView(corpus, { path: 'photos/2024/none' })
    expect(deep.fileCount).toBe(0)
  })
})

describe('relativeDir', () => {
  it('搜索结果显示当前目录以下的相对路径', () => {
    const f = file('photos/2024/b.jpg')
    expect(relativeDir(f, 'photos')).toBe('2024')
    expect(relativeDir(f, '')).toBe('photos/2024')
    expect(relativeDir(file('a.jpg'), '')).toBe('')
  })
})
