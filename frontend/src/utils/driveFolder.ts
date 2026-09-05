// 网盘目录视图：从对象存储的扁平 key 列表推导文件夹导航（Cloudreve 对标
// 「轻量目录层」的客户端最小实现——权威目录层将来落控制面，本模块只做视图推导，
// 不引入第二个真相源）。
//
// 关键语义：
// - key 即路径（"photos/2024/img.jpg"），目录 = 去掉末段的 key 前缀；
// - "dir/" 形式的零字节目录占位对象只参与文件夹推导，不作为文件出现；
// - 搜索跨当前目录的全部后代（网盘产品的通识行为），命中文件平铺；
// - 文件夹恒排在文件前，文件夹之间按名称排序。
import type { CloudFile } from '../types/core'

export interface DriveFolder {
  name: string
  path: string // 相对根的目录路径（不带首尾斜杠，'' = 根）
}

export interface DriveCrumb {
  name: string
  path: string
}

export type DriveSortKey = 'name' | 'size' | 'lastModified'
export type DriveSortDir = 'asc' | 'desc'

export interface DriveViewOptions {
  path: string
  query?: string
  sortKey?: DriveSortKey
  sortDir?: DriveSortDir
}

export interface DriveView {
  breadcrumbs: DriveCrumb[]
  folders: DriveFolder[]
  files: CloudFile[]
  folderCount: number
  fileCount: number
  totalSize: number
  searching: boolean
}

const normalizePath = (path: string) => path.replace(/^\/+|\/+$/g, '')

const parentOf = (key: string): string => {
  const index = key.lastIndexOf('/')
  return index < 0 ? '' : key.slice(0, index)
}

const nameOf = (key: string): string => key.slice(key.lastIndexOf('/') + 1)

// path 在 current 之下（含等值）时，返回相对 current 的第一段；否则 null。
const firstSegmentUnder = (path: string, current: string): string | null => {
  if (current === '') {
    return path.split('/')[0] || null
  }
  if (!path.startsWith(`${current}/`)) {
    return null
  }
  return path.slice(current.length + 1).split('/')[0] || null
}

// 面包屑：根 → 当前目录（首项固定「网盘」，当前目录自身含在列表里）
export function breadcrumbsFor(path: string): DriveCrumb[] {
  const crumbs: DriveCrumb[] = [{ name: '网盘', path: '' }]
  let walked = ''
  for (const segment of normalizePath(path).split('/').filter(Boolean)) {
    walked = walked ? `${walked}/${segment}` : segment
    crumbs.push({ name: segment, path: walked })
  }
  return crumbs
}

// 命中文件在搜索结果里的显示路径：当前目录以下的剩余路径（根目录搜索时即全路径）
export function relativeDir(file: CloudFile, basePath: string): string {
  const parent = parentOf(file.key)
  const base = normalizePath(basePath)
  if (!parent) return ''
  if (!base) return parent
  return parent.startsWith(`${base}/`) ? parent.slice(base.length + 1) : parent
}

export function buildDriveView(files: CloudFile[], options: DriveViewOptions): DriveView {
  const current = normalizePath(options.path)
  const query = (options.query || '').trim().toLowerCase()
  const sortKey = options.sortKey || 'lastModified'
  const sortDir = options.sortDir || 'desc'
  const searching = query.length > 0

  const folderNames = new Set<string>()
  const matched: CloudFile[] = []
  let totalSize = 0

  for (const file of files) {
    const isDirMarker = /\/$/.test(file.key)
    const key = file.key.replace(/\/+$/, '')
    const name = nameOf(key)
    if (!name) continue

    const parent = parentOf(key)
    const inScope =
      current === '' || parent === current || parent.startsWith(`${current}/`)

    if (isDirMarker) {
      // 目录占位：只贡献文件夹，不作为文件
      if (!searching && inScope) {
        const first = firstSegmentUnder(key, current)
        if (first) folderNames.add(first)
      }
      continue
    }

    if (searching) {
      if (inScope && name.toLowerCase().includes(query)) {
        matched.push(file)
        totalSize += file.size
      }
      continue
    }

    if (parent === current) {
      matched.push(file)
      totalSize += file.size
      continue
    }
    if (inScope) {
      const first = firstSegmentUnder(parent, current)
      if (first) folderNames.add(first)
    }
  }

  const folders: DriveFolder[] = searching
    ? []
    : Array.from(folderNames)
        .sort((a, b) => a.localeCompare(b, 'zh-Hans-CN'))
        .map(name => ({ name, path: current ? `${current}/${name}` : name }))

  matched.sort(compareBy(sortKey, sortDir))

  return {
    breadcrumbs: breadcrumbsFor(current),
    folders,
    files: matched,
    folderCount: folders.length,
    fileCount: matched.length,
    totalSize,
    searching,
  }
}

function compareBy(sortKey: DriveSortKey, sortDir: DriveSortDir) {
  const dir = sortDir === 'asc' ? 1 : -1
  return (a: CloudFile, b: CloudFile) => {
    if (sortKey === 'name') {
      return a.name.localeCompare(b.name, 'zh-Hans-CN') * dir
    }
    if (sortKey === 'size') {
      return (a.size - b.size) * dir
    }
    return (new Date(a.lastModified).getTime() - new Date(b.lastModified).getTime()) * dir
  }
}
