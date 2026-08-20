"""目录监听自动入库：把监听目录里的新文件/改动文件自动流入知识库。

文件沼泽一键变知识的引擎（发芽路线）。轮询实现——watchdog 的文件系统事件
在 SMB 共享盘/网络映射盘上不可靠，轮询对本地与共享盘行为一致。
幂等靠内容哈希版本号：同内容重复扫描不会重复入库（job 幂等 + 版本去重）。
"""
from app.watcher.service import DirectoryWatcher

__all__ = ["DirectoryWatcher"]
