-- 桌面端长期登录（2026-10-25 调整为滑动语义）：30 天内使用过就免登录。
-- timeout=365 天是绝对寿命上限（防永久令牌）；active_timeout=30 天才是实际门槛——
-- 持续使用者永不过期，连续 30 天不用才需重登。若把 timeout 也设 30 天，天天用的员工
-- 每满 30 天仍会被强制重登一次，不满足「一个月内登录过即免登录」。
-- 长寿命的安全前提：桌面端每次启动都向控制面校验会话（app.go validateAccountSession），
-- 停用账号/踢会话立即生效；改密不踢旧令牌（RuoYi updatePwd 无踢出逻辑），异常时用
-- 后台「在线用户」手动踢。
-- 幂等，可重复执行；新部署由 ship-control-plane.ps1 自动投递。
-- 注意：只影响新登录签发的令牌，已在线令牌的 TTL 在签发时已定，老令牌到期后重登一次即进入新窗口。
UPDATE sys_client
SET timeout = 31536000,
    active_timeout = 2592000
WHERE client_id = 'e5cd7e4891bf95d1d19206ce24a7b32e';
