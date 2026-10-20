-- 桌面端长期登录：PC 客户端 30 天绝对有效期 + 30 天空闲有效期。
-- RuoYi 默认 7 天 / 30 分钟；30 分钟空闲会让关掉客户端半小时后必须重登。
-- 幂等，可重复执行；新部署由 ship-control-plane.ps1 自动投递。
UPDATE sys_client
SET timeout = 2592000,
    active_timeout = 2592000
WHERE client_id = 'e5cd7e4891bf95d1d19206ce24a7b32e';
