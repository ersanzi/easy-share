"""薄控制面 2a：账号与登录（默认关闭，见 product-positioning.md 发芽路线）。

路由在 app.auth.routes（由 main.py 挂载）；本包 __init__ 只导出存储层，
避免 services ←→ routes 循环导入。
"""
from app.auth.store import UserStore

__all__ = ["UserStore"]
