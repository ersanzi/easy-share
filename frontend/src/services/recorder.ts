// 会议录音采集：getUserMedia + AudioWorklet（降级 ScriptProcessor）→ 16kHz 单声道 PCM → WAV。
// Windows：Wails v2.14 已默认放行 WebView2 全部权限（SetGlobalPermission(Allow)），开箱即用；
// macOS：Info.plist 声明 NSMicrophoneUsageDescription + WailsContext 媒体权限 category（TCC 弹窗）。
// 输出 16k/单声道/s16 WAV 直接对接 FunASR，不经 MediaRecorder（避免服务端再引 ffmpeg 解 webm）。

export interface MeetingRecording {
  blob: Blob
  durationSeconds: number
}

// 录音音源：mic=麦克风（双平台）；system=系统声音+麦克风混流（Windows，macOS 隐藏入口）。
// system 走钉钉听记同款做法（逆向证据 E9）：把 getDisplayMedia 的系统音频轨与麦克风轨
// connect 到同一个 MediaStreamAudioDestinationNode，合成一路音再采样——后端只见一路音，
// 且线上会议同时录到对方发言与自己的发言。
export type RecordingSource = 'mic' | 'system'

interface CaptureHandler {
  (samples: Float32Array): void
}

const WORKLET_CODE = `
class EshareCapture extends AudioWorkletProcessor {
  process(inputs) {
    const input = inputs[0];
    if (input && input[0] && input[0].length) this.port.postMessage(input[0]);
    return true;
  }
}
registerProcessor('eshare-capture', EshareCapture);
`

const TARGET_RATE = 16000
// 实时推流单元：与知识服务流式模型对齐（600ms = 9600 样本 = 19200 字节 s16le）
export const STREAM_CHUNK_SAMPLES = 9600

export type ChunkHandler = (pcm: Uint8Array) => void

export class MeetingRecorder {
  private stream: MediaStream | null = null
  private micStream: MediaStream | null = null
  private displayStream: MediaStream | null = null
  private context: AudioContext | null = null
  private source: MediaStreamAudioSourceNode | null = null
  private worklet: AudioWorkletNode | null = null
  private processor: ScriptProcessorNode | null = null
  private chunks: Int16Array[] = []
  private totalFrames = 0
  private startedAt = 0
  private lastLevelAt = 0
  private chunkHandler: ChunkHandler | null = null
  private carry: Int16Array[] = []
  private carryFrames = 0

  recording = false

  // 系统声音共享被用户从浏览器条「停止共享」时触发（UI 侧应收尾本次录音）
  onAudioEnded: (() => void) | null = null

  async start(
    source: RecordingSource = 'mic',
    onLevel?: (level: number) => void,
    onChunk?: ChunkHandler,
  ): Promise<void> {
    if (this.recording) return
    this.chunkHandler = onChunk ?? null
    this.carry = []
    this.carryFrames = 0
    this.micStream = await navigator.mediaDevices.getUserMedia({
      audio: { channelCount: 1, echoCancellation: true, noiseSuppression: true, autoGainControl: true },
    })
    // 刻意请求 16kHz 上下文；浏览器不给就手动降采样
    let context: AudioContext
    try {
      context = new AudioContext({ sampleRate: TARGET_RATE })
    } catch {
      context = new AudioContext()
    }
    this.context = context

    let sampled = this.micStream
    if (source === 'system') {
      try {
        this.displayStream = await this.acquireSystemAudio()
      } catch (e) {
        // 共享选择器被取消：不能让麦克风与 AudioContext 悬着（系统麦克风灯会一直亮）
        this.releaseStreams()
        void this.context?.close()
        this.context = null
        throw e
      }
      sampled = this.mixInto(context, this.micStream, this.displayStream)
    }
    this.stream = sampled
    this.bindEndedSignal(source === 'system' ? this.displayStream : this.micStream)

    this.source = context.createMediaStreamSource(sampled)
    this.source.channelCount = 1
    this.source.channelCountMode = 'explicit'
    this.chunks = []
    this.totalFrames = 0
    this.startedAt = Date.now()

    const handler: CaptureHandler = samples => this.consume(samples, context.sampleRate, onLevel)
    let workletReady = false
    if (context.audioWorklet) {
      try {
        const url = URL.createObjectURL(new Blob([WORKLET_CODE], { type: 'application/javascript' }))
        await context.audioWorklet.addModule(url)
        URL.revokeObjectURL(url)
        const node = new AudioWorkletNode(context, 'eshare-capture')
        node.port.onmessage = event => handler(event.data as Float32Array)
        this.source.connect(node)
        this.worklet = node
        workletReady = true
      } catch {
        workletReady = false
      }
    }
    if (!workletReady) {
      // 兜底：ScriptProcessor 已废弃但 WebView2/WKWebView 都还支持。
      // 经零增益节点接回 destination——只为满足拉流，不能把采到的声音真放出来（否则啸叫）
      const mute = context.createGain()
      mute.gain.value = 0
      mute.connect(context.destination)
      const processor = context.createScriptProcessor(4096, 1, 1)
      processor.onaudioprocess = event => handler(event.inputBuffer.getChannelData(0))
      this.source.connect(processor)
      processor.connect(mute)
      this.processor = processor
    }
    this.recording = true
  }

  // 混流：麦克风轨 + 系统声音轨 → 同一个 destination，产出单声道合成流
  private mixInto(context: AudioContext, mic: MediaStream, display: MediaStream): MediaStream {
    const destination = context.createMediaStreamDestination()
    destination.channelCount = 1
    destination.channelCountMode = 'explicit'
    context.createMediaStreamSource(mic).connect(destination)
    const systemStream = new MediaStream(display.getAudioTracks())
    context.createMediaStreamSource(systemStream).connect(destination)
    return destination.stream
  }

  // 用户从浏览器共享条「停止共享」时音频轨 ended，需要收尾本次录音
  private bindEndedSignal(origin: MediaStream | null): void {
    origin?.getAudioTracks().forEach(track => {
      track.onended = () => {
        if (this.recording && this.onAudioEnded) this.onAudioEnded()
      }
    })
  }

  // 系统声音：走屏幕共享拿系统音频轨（WebView2/Chromium 会弹共享选择器，
  // 勾选「同时分享系统音频」才有音轨）。video 轨必须保留到停止——Chromium 里
  // 关掉 video 轨会连带结束整个共享会话（音频轨一起死）。
  private async acquireSystemAudio(): Promise<MediaStream> {
    if (!navigator.mediaDevices?.getDisplayMedia) {
      throw new Error('当前环境不支持系统声音采集（该能力仅 Windows 提供）')
    }
    let stream: MediaStream
    try {
      stream = await navigator.mediaDevices.getDisplayMedia({
        video: true,
        audio: { echoCancellation: false, noiseSuppression: false, autoGainControl: false },
        // Chromium 专有选项（类型未收录）：共享时呈现系统音频勾选项、排除本页自身
        systemAudio: 'include',
        selfBrowserSurface: 'exclude',
      } as DisplayMediaStreamOptions)
    } catch (e) {
      const name = (e as DOMException)?.name
      if (name === 'NotAllowedError') {
        throw new Error('已取消屏幕/声音共享选择，未开始录音')
      }
      throw e
    }
    if (stream.getAudioTracks().length === 0) {
      stream.getTracks().forEach(track => track.stop())
      throw new Error('共享时未勾选「同时分享系统音频」，没有拿到系统声音，请重试并勾选音频')
    }
    return stream
  }

  async stop(): Promise<MeetingRecording> {
    this.recording = false
    this.worklet?.disconnect()
    this.processor?.disconnect()
    this.source?.disconnect()
    this.releaseStreams()
    const durationSeconds = this.totalFrames / TARGET_RATE
    await this.context?.close()
    this.worklet = null
    this.processor = null
    this.source = null
    this.context = null
    return { blob: encodeWav(this.chunks, TARGET_RATE), durationSeconds }
  }

  abort(): void {
    this.recording = false
    this.worklet?.disconnect()
    this.processor?.disconnect()
    this.source?.disconnect()
    this.releaseStreams()
    void this.context?.close()
    this.worklet = null
    this.processor = null
    this.source = null
    this.context = null
    this.chunks = []
    this.totalFrames = 0
  }

  // 混流模式下 this.stream 是 destination 的合成流（停它没用），必须停掉来源轨道；
  // 共享的 video 轨也要停，否则 Chromium 顶部「正在共享屏幕」提示条不消失
  private releaseStreams(): void {
    for (const stream of [this.stream, this.micStream, this.displayStream]) {
      stream?.getTracks().forEach(track => track.stop())
    }
    this.stream = null
    this.micStream = null
    this.displayStream = null
  }

  elapsedSeconds(): number {
    return this.recording ? Math.floor((Date.now() - this.startedAt) / 1000) : 0
  }

  private consume(samples: Float32Array, sampleRate: number, onLevel?: (level: number) => void): void {
    if (!this.recording) return
    const pcm = sampleRate === TARGET_RATE ? samples : resample(samples, sampleRate, TARGET_RATE)
    const int16 = new Int16Array(pcm.length)
    let sumSquares = 0
    for (let i = 0; i < pcm.length; i++) {
      const clamped = Math.max(-1, Math.min(1, pcm[i]))
      int16[i] = clamped < 0 ? clamped * 0x8000 : clamped * 0x7fff
      sumSquares += clamped * clamped
    }
    this.chunks.push(int16)
    this.totalFrames += int16.length
    this.emitChunk(int16)
    // 电平节流到 ~10 次/秒，驱动波形动画足够
    const now = Date.now()
    if (onLevel && now - this.lastLevelAt > 100) {
      this.lastLevelAt = now
      onLevel(Math.sqrt(sumSquares / pcm.length))
    }
  }

  // 攒够 600ms 就吐一片给实时推流（不足一片的尾巴留在 carry 里，下次补齐）
  private emitChunk(pcm: Int16Array): void {
    if (!this.chunkHandler || pcm.length === 0) return
    this.carry.push(pcm)
    this.carryFrames += pcm.length
    while (this.carryFrames >= STREAM_CHUNK_SAMPLES) {
      const merged = new Int16Array(this.carryFrames)
      let offset = 0
      for (const piece of this.carry) {
        merged.set(piece, offset)
        offset += piece.length
      }
      const take = merged.subarray(0, STREAM_CHUNK_SAMPLES)
      const rest = merged.subarray(STREAM_CHUNK_SAMPLES)
      this.carry = rest.length ? [new Int16Array(rest)] : []
      this.carryFrames = rest.length
      // Int16Array → 小端字节流（WebAudio 与知识服务口径一致，不做字节序转换）
      this.chunkHandler(new Uint8Array(take.buffer, take.byteOffset, take.byteLength).slice())
    }
  }
}

// 线性插值降采样（48k→16k 质量足够语音）
function resample(samples: Float32Array, from: number, to: number): Float32Array {
  const ratio = from / to
  const length = Math.floor(samples.length / ratio)
  const output = new Float32Array(length)
  for (let i = 0; i < length; i++) {
    const position = i * ratio
    const index = Math.floor(position)
    const fraction = position - index
    const current = samples[index] ?? 0
    const next = samples[index + 1] ?? current
    output[i] = current + (next - current) * fraction
  }
  return output
}

// 标准 44 字节头的 PCM WAV 编码
function encodeWav(chunks: Int16Array[], sampleRate: number): Blob {
  const totalFrames = chunks.reduce((sum, chunk) => sum + chunk.length, 0)
  const dataSize = totalFrames * 2
  const buffer = new ArrayBuffer(44 + dataSize)
  const view = new DataView(buffer)
  const writeText = (offset: number, text: string) => {
    for (let i = 0; i < text.length; i++) view.setUint8(offset + i, text.charCodeAt(i))
  }
  writeText(0, 'RIFF')
  view.setUint32(4, 36 + dataSize, true)
  writeText(8, 'WAVE')
  writeText(12, 'fmt ')
  view.setUint32(16, 16, true)
  view.setUint16(20, 1, true) // PCM
  view.setUint16(22, 1, true) // 单声道
  view.setUint32(24, sampleRate, true)
  view.setUint32(28, sampleRate * 2, true) // 字节率
  view.setUint16(32, 2, true) // 块对齐
  view.setUint16(34, 16, true) // 位深
  writeText(36, 'data')
  view.setUint32(40, dataSize, true)
  let offset = 44
  for (const chunk of chunks) {
    new Int16Array(buffer, offset, chunk.length).set(chunk)
    offset += chunk.length * 2
  }
  return new Blob([buffer], { type: 'audio/wav' })
}

// ─── 导入已有音频文件（对标钉钉听记「导入文件」）───

export interface DecodedAudioFile {
  blob: Blob
  durationSeconds: number
}

// 浏览器本地解码（wav/mp3/m4a/aac/flac/ogg，WebView2 系统解码器）→ 下混单声道
// → 16k WAV。产出与录音完全同构，复用既有上传通道，后端零改动。
export async function decodeAudioFileToWav(file: File): Promise<DecodedAudioFile> {
  const bytes = await file.arrayBuffer()
  const context = new AudioContext()
  let decoded: AudioBuffer
  try {
    decoded = await context.decodeAudioData(bytes)
  } catch {
    throw new Error('无法解码该音频文件（支持 wav/mp3/m4a/flac/ogg），请换一个文件')
  } finally {
    void context.close()
  }
  const mono = new Float32Array(decoded.length)
  for (let channel = 0; channel < decoded.numberOfChannels; channel++) {
    const data = decoded.getChannelData(channel)
    for (let i = 0; i < mono.length; i++) mono[i]! += data[i]!
  }
  const scale = 1 / decoded.numberOfChannels
  for (let i = 0; i < mono.length; i++) mono[i]! *= scale
  const pcm = decoded.sampleRate === TARGET_RATE ? mono : resample(mono, decoded.sampleRate, TARGET_RATE)
  const int16 = new Int16Array(pcm.length)
  for (let i = 0; i < pcm.length; i++) {
    const clamped = Math.max(-1, Math.min(1, pcm[i]!))
    int16[i] = clamped < 0 ? clamped * 0x8000 : clamped * 0x7fff
  }
  return { blob: encodeWav([int16], TARGET_RATE), durationSeconds: decoded.duration }
}
