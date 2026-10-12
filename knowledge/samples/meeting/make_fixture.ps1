# 会议听记测试夹具：分句合成 16kHz/16bit/单声道 WAV（说话人交替发言）
# 用法: powershell -ExecutionPolicy Bypass -File make_fixture.ps1 -OutDir <目录>
# 之后用同目录 concat_fixture.py 拼接为 meeting_test.wav
param([Parameter(Mandatory=$true)][string]$OutDir)
Add-Type -AssemblyName System.Speech
New-Item -ItemType Directory -Force -Path $OutDir | Out-Null

# 元素：声部名 | 语速(-10..10) | 段名 | 文本 —— 说话人 A=Huihui 语速 0，B=Huihui 语速 -2（同声部不同语速，
# 刻意构成对声纹聚类的压力测试），B5 用 Zira 英文（真正不同音色）
$lines = @(
  @('Microsoft Huihui Desktop', 0,  'A1', '各位同事，我们开始今天的周会，主要过一下知识平台部署和客户演示的准备情况。'),
  @('Microsoft Huihui Desktop', -2, 'B1', '好的，服务器这边已经就绪，容器和对象存储都升级到了最新版本，部署清单我昨天已经过了一遍。'),
  @('Microsoft Huihui Desktop', 0,  'A2', '很好，那文档入库的进度怎么样？客户那边希望周五之前能看到完整的知识库。'),
  @('Microsoft Huihui Desktop', -2, 'B2', '目前已经入库了大概三百份文档，主要是产品手册和常用的工艺文件，识别准确率看起来不错。'),
  @('Microsoft Huihui Desktop', 0,  'A3', '明天的客户演示，我建议重点演示知识问答和会议纪要这两个功能，演示脚本谁来做？'),
  @('Microsoft Huihui Desktop', -2, 'B3', '演示脚本我来准备，今天下班前发到群里，大家有空帮我把把关。'),
  @('Microsoft Huihui Desktop', 0,  'A4', '另外，会议听记功能上线后，我们可以直接用会议录音自动生成纪要，不用再手动整理了。'),
  @('Microsoft Huihui Desktop', -2, 'B4', '对，这个功能对销售团队特别有用，每次拜访客户回来都要花很久写会议记录。'),
  @('Microsoft Huihui Desktop', 0,  'A5', '好，那就这么定了。我总结一下：服务器部署完成，文档入库继续推进，演示脚本今天出初稿。'),
  @('Microsoft Zira Desktop',   0,  'B5', 'OK, I will send the demo checklist by email tonight and share it in the group.')
)

foreach ($line in $lines) {
  $voice = $line[0]; $rate = [int]$line[1]; $name = $line[2]; $text = $line[3]
  $synth = New-Object System.Speech.Synthesis.SpeechSynthesizer
  $synth.SelectVoice($voice)
  $synth.Rate = $rate
  $fmt = New-Object System.Speech.AudioFormat.SpeechAudioFormatInfo(16000, [System.Speech.AudioFormat.AudioBitsPerSample]::Sixteen, [System.Speech.AudioFormat.AudioChannel]::Mono)
  $out = Join-Path $OutDir ($name + '.wav')
  $synth.SetOutputToWaveFile($out, $fmt)
  $synth.Speak($text)
  $synth.Dispose()
  Write-Host ("generated " + $out)
}
