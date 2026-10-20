import { beforeEach, describe, expect, it, vi } from 'vitest'

// 录音会话是模块单例：测试里换成可控的采集器与 Core 桩，
// 重点验证「切页/卸载不会中断录音」「推流失败要能续上而不是收摊」这两条产品约束。
const recorderState = vi.hoisted(() => ({ instance: null as any }))

vi.mock('../core', () => ({
  core: {
    meetingStreamOpen: vi.fn(),
    meetingStreamWarm: vi.fn(),
    meetingStreamChunk: vi.fn(),
    meetingStreamStop: vi.fn(),
    meetingLiveSession: vi.fn(),
    meetingLiveCheck: vi.fn(),
    meetingUpload: vi.fn(),
    meetingRemove: vi.fn(),
    meetingLivePoints: vi.fn(),
  },
}))

vi.mock('../recorder', () => ({
  STREAM_CALL_TIMEOUT_MS: 12000,
  MeetingRecorder: class {
    onAudioEnded: (() => void) | null = null
    recording = false
    chunkHandler: ((pcm: Uint8Array) => void) | null = null
    start = vi.fn(async (_source: string, _level?: (value: number) => void, onChunk?: (pcm: Uint8Array) => void) => {
      this.recording = true
      this.chunkHandler = onChunk ?? null
    })
    stop = vi.fn(async () => ({ blob: new Blob(['wav']), durationSeconds: 12 }))
    abort = vi.fn()
    elapsedSeconds = vi.fn(() => 12)
    constructor() {
      recorderState.instance = this
    }
  },
}))

const liveView = (lines: { text: string; at: number }[] = [], current = '') => ({
  lines,
  current,
  elapsed_seconds: 12,
})

// 每次取一份全新模块图：单例状态与 mock 调用记录都从零开始
const load = async () => {
  vi.resetModules()
  const { core } = await import('../core')
  const { meetingSession } = await import('../meetingSession')
  return {
    core: core as unknown as {
      meetingStreamOpen: ReturnType<typeof vi.fn>
      meetingStreamWarm: ReturnType<typeof vi.fn>
      meetingStreamChunk: ReturnType<typeof vi.fn>
      meetingStreamStop: ReturnType<typeof vi.fn>
      meetingLiveSession: ReturnType<typeof vi.fn>
      meetingLiveCheck: ReturnType<typeof vi.fn>
      meetingUpload: ReturnType<typeof vi.fn>
      meetingRemove: ReturnType<typeof vi.fn>
      meetingLivePoints: ReturnType<typeof vi.fn>
    },
    session: meetingSession,
  }
}

describe('meetingSession', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.useRealTimers()
  })

  it('切页卸载不会中断录音，停止后复用一个会话上传', async () => {
    const { core, session } = await load()
    core.meetingStreamOpen.mockResolvedValue({ id: 'm1' })
    core.meetingStreamChunk.mockResolvedValue(liveView())
    core.meetingStreamStop.mockResolvedValue(liveView([{ text: '最后一句', at: 12 }]))
    core.meetingUpload.mockResolvedValue({ id: 'm1' })

    await session.start()
    expect(session.recording.value).toBe(true)
    expect(core.meetingStreamOpen).toHaveBeenCalledTimes(1)
    expect(session.liveState.value).toBe('warming')

    await session.stop()
    expect(session.recording.value).toBe(false)
    expect(session.uploading.value).toBe(false)
    expect(core.meetingStreamStop).toHaveBeenCalledWith('m1')
    expect(core.meetingUpload).toHaveBeenCalledWith(expect.any(Blob), expect.any(String), expect.any(String), 'm1')
    expect(session.finishedMeetingId.value).toBe('m1')
    expect(session.takeFinished()).toBe('m1')
    expect(session.finishedMeetingId.value).toBe('')
  })

  it('单次推流失败不中断：先从会话快照续上，再继续推后面的片', async () => {
    const { core, session } = await load()
    core.meetingStreamOpen.mockResolvedValue({ id: 'm1' })
    // 第一片失败，第二片照常成功：推流队列不能因为一次错误就丢弃
    let call = 0
    core.meetingStreamChunk.mockImplementation(async () => {
      call += 1
      if (call === 1) throw new Error('网络抖动')
      return liveView([{ text: '回来的字', at: 2 }])
    })
    core.meetingLiveSession.mockResolvedValue({ recording: true, lines: [{ text: '续上的字', at: 1 }], current: '', elapsed_seconds: 2 })
    core.meetingStreamStop.mockResolvedValue(liveView())
    core.meetingUpload.mockResolvedValue({ id: 'm1' })

    await session.start()
    recorderState.instance.chunkHandler?.(new Uint8Array(19200))
    recorderState.instance.chunkHandler?.(new Uint8Array(19200))
    await vi.waitFor(() => {
      expect(core.meetingStreamChunk).toHaveBeenCalledTimes(2)
      expect(session.liveLines.value.map(line => line.text)).toContain('回来的字')
    })
    // 快照续上的字出现过（不是「一断就清空」），且推流会话没有被标成不可用
    expect(core.meetingLiveSession).toHaveBeenCalledWith('m1')
    expect(session.liveState.value).not.toBe('unavailable')
    expect(session.recording.value).toBe(true)

    await session.stop()
    expect(core.meetingUpload).toHaveBeenCalledTimes(1)
  })

  it('连续多次失败才降级为轮询兜底，录音不停', async () => {
    const { core, session } = await load()
    core.meetingStreamOpen.mockResolvedValue({ id: 'm1' })
    core.meetingStreamChunk.mockRejectedValue(new Error('网络断开'))
    core.meetingLiveSession.mockResolvedValue({ recording: true, lines: [{ text: '兜底字幕', at: 3 }], current: '', elapsed_seconds: 3 })
    core.meetingStreamStop.mockResolvedValue(liveView())
    core.meetingUpload.mockResolvedValue({ id: 'm1' })

    await session.start()
    // 推流失败是异步发生的：等这次失败落定后再推下一片，避免多片并发互相覆盖失败计数
    for (let index = 0; index < 3; index += 1) {
      recorderState.instance.chunkHandler?.(new Uint8Array(19200))
      await vi.waitFor(() => {
        expect(core.meetingStreamChunk).toHaveBeenCalledTimes(index + 1)
        expect(core.meetingLiveSession).toHaveBeenCalledTimes(index + 1)
      })
    }
    await vi.waitFor(() => {
      expect(session.liveState.value).toBe('degraded')
    })
    expect(session.liveLines.value.map(line => line.text)).toContain('兜底字幕')
    expect(session.recording.value).toBe(true)

    await session.stop()
    expect(core.meetingUpload).toHaveBeenCalledTimes(1)
  })

  it('上屏后按节流自动快检，结果入列', async () => {
    const { core, session } = await load()
    core.meetingStreamOpen.mockResolvedValue({ id: 'm1' })
    const manyLines = Array.from({ length: 8 }, (_, index) => ({ text: `第${index}句`, at: index }))
    core.meetingStreamChunk.mockResolvedValueOnce(liveView(manyLines))
    core.meetingLiveCheck.mockResolvedValue({
      verdict: 'attention',
      note: '期限前后不一',
      points: ['前面说周四'],
      last_line_at: 7,
      checked_at: '10:00:01',
    })
    core.meetingLivePoints.mockResolvedValue([])
    core.meetingStreamStop.mockResolvedValue(liveView())
    core.meetingUpload.mockResolvedValue({ id: 'm1' })

    await session.start()
    recorderState.instance.chunkHandler?.(new Uint8Array(19200))
    await vi.waitFor(() => {
      expect(session.checks.value).toHaveLength(1)
    })
    expect(session.checks.value[0].verdict).toBe('attention')
    expect(core.meetingLiveCheck).toHaveBeenCalledWith('m1')
    await session.stop()
  })

  it('取消录音会删掉刚建的实时会话，不留孤儿记录', async () => {
    const { core, session } = await load()
    core.meetingStreamOpen.mockResolvedValue({ id: 'm1' })
    core.meetingRemove.mockResolvedValue(undefined)

    await session.start()
    await session.cancel()
    expect(session.recording.value).toBe(false)
    expect(recorderState.instance.abort).toHaveBeenCalledTimes(1)
    expect(core.meetingRemove).toHaveBeenCalledWith('m1')
    expect(core.meetingUpload).not.toHaveBeenCalled()
  })
})
