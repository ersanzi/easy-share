"""会议听记：录音上传 → FunASR 转写与说话人分离 → LLM 总结。

重依赖（funasr/torch）在 requirements-asr.txt 单列，`ASR_ENABLED` 默认关闭；
引擎在首个转写任务时才拉起模型，不影响无 ASR 部署的启动速度。

注意：本包 __init__ 刻意不导出 routes——routes 依赖 app.services，
而 services 反向导入本包叶子模块，急切导出会构成循环导入；
入口在 main.py 直接 `from app.meetings.routes import router`（与 lab 同款）。
"""
from app.meetings.engine import MeetingTranscriber
from app.meetings.service import MeetingService
from app.meetings.store import MeetingStore
from app.meetings.summarizer import MeetingSummarizer

__all__ = [
    "MeetingService",
    "MeetingStore",
    "MeetingSummarizer",
    "MeetingTranscriber",
]
