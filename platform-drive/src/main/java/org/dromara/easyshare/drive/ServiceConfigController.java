package org.dromara.easyshare.drive;

import cn.dev33.satoken.annotation.SaCheckLogin;
import cn.dev33.satoken.annotation.SaCheckRole;
import com.baomidou.mybatisplus.core.conditions.query.LambdaQueryWrapper;
import lombok.RequiredArgsConstructor;
import org.dromara.common.core.domain.R;
import org.dromara.easyshare.drive.domain.SysConfigRow;
import org.dromara.easyshare.drive.mapper.SysConfigRowMapper;
import org.springframework.validation.annotation.Validated;
import org.springframework.web.bind.annotation.*;

/**
 * 租户服务配置下发接口。
 * <p>
 * 设计动因：客户端只应预知**一个**地址（控制面，构建期烧录），其余服务拓扑
 * （知识服务等）登录后从这里拿——服务器换 IP、加服务都不用重新发安装包。
 * 存储复用 sys_config（零 DDL），键：{@value #KNOWLEDGE_URL_KEY}。
 * <p>
 * 权限：读是登录即可（每个同事的客户端登录后都要拉）；写仅超管（管理员在
 * 客户端管理页登记一次）。
 *
 * @author EasyShare
 */
@Validated
@SaCheckLogin
@RestController
@RequiredArgsConstructor
@RequestMapping("/easyshare/service")
public class ServiceConfigController {

    /** 知识服务地址的配置键（sys_config.config_key） */
    public static final String KNOWLEDGE_URL_KEY = "drive.service.knowledge.url";

    private final SysConfigRowMapper configMapper;

    /**
     * 下发租户服务配置。未登记的项返回空串，客户端自行回退（同主机推导默认端口）。
     */
    @GetMapping("/config")
    public R<ServiceConfigVo> config() {
        return R.ok(new ServiceConfigVo(configValue(KNOWLEDGE_URL_KEY)));
    }

    /**
     * 登记服务配置（超管）。body 里留空即清除该项，客户端回退推导值。
     */
    @SaCheckRole("superadmin")
    @PutMapping("/config")
    public R<Void> saveConfig(@Validated @RequestBody SaveBo body) {
        upsert(KNOWLEDGE_URL_KEY, "知识服务地址", normalizeUrl(body.knowledgeUrl()));
        return R.ok();
    }

    /** 读配置值；行不存在返回空串 */
    private String configValue(String key) {
        SysConfigRow row = configMapper.selectOne(new LambdaQueryWrapper<SysConfigRow>()
            .eq(SysConfigRow::getConfigKey, key));
        return row == null || row.getConfigValue() == null ? "" : row.getConfigValue().trim();
    }

    /** 按 key upsert：行存在改值，不存在建行（configId 走全局雪花分配） */
    private void upsert(String key, String name, String value) {
        SysConfigRow row = configMapper.selectOne(new LambdaQueryWrapper<SysConfigRow>()
            .eq(SysConfigRow::getConfigKey, key));
        if (row == null) {
            row = new SysConfigRow();
            row.setConfigName(name);
            row.setConfigKey(key);
            row.setConfigValue(value);
            row.setConfigType("N");
            row.setRemark("EasyShare 租户服务配置（客户端管理页维护）");
            configMapper.insert(row);
        } else {
            row.setConfigValue(value);
            configMapper.updateById(row);
        }
    }

    /** 去空白、去尾斜杠；空串放行（清除语义），非空必须是 http(s) 地址 */
    private String normalizeUrl(String url) {
        String trimmed = url == null ? "" : url.trim();
        if (trimmed.isEmpty()) {
            return "";
        }
        while (trimmed.endsWith("/")) {
            trimmed = trimmed.substring(0, trimmed.length() - 1);
        }
        if (!trimmed.startsWith("http://") && !trimmed.startsWith("https://")) {
            throw new IllegalArgumentException("地址必须以 http:// 或 https:// 开头");
        }
        return trimmed;
    }

    public record ServiceConfigVo(String knowledgeUrl) {
    }

    public record SaveBo(String knowledgeUrl) {
    }
}
