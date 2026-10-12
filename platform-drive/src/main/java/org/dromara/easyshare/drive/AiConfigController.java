package org.dromara.easyshare.drive;

import cn.dev33.satoken.annotation.SaCheckLogin;
import cn.dev33.satoken.annotation.SaCheckRole;
import com.baomidou.mybatisplus.core.conditions.query.LambdaQueryWrapper;
import lombok.RequiredArgsConstructor;
import lombok.extern.slf4j.Slf4j;
import org.dromara.common.core.domain.R;
import org.dromara.easyshare.drive.domain.SysConfigRow;
import org.dromara.easyshare.drive.mapper.SysConfigRowMapper;
import org.springframework.validation.annotation.Validated;
import org.springframework.web.bind.annotation.*;

import java.net.URI;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.time.Duration;
import java.util.regex.Matcher;
import java.util.regex.Pattern;

/**
 * AI 能力配置（管理端配置化）：LLM 供应商/模型/密钥与听记开关。
 * <p>
 * 设计：sys_config 是权威源（单键 JSON，{@value #AI_CONFIG_KEY}），桌面端管理页
 * 经这里读写；保存后同步推送到知识服务 {@code PUT /admin/ai-config} 热生效，
 * 推送鉴权用共享服务密钥（{@value #AI_SECRET_KEY}，deploy 脚本生成、与知识服务
 * .env CONTROL_PLANE_SERVICE_SECRET 同值）。推送失败不阻断保存——配置已入
 * sys_config，重新保存即重推。
 * <p>
 * 权限：读写均仅超管（配置含 LLM 密钥）。密钥明文存 sys_config 的边界：同机
 * 部署下 DB 访问本就等价于持有密钥（与 .env 同级），访问面用超管收口。
 * <p>
 * JSON 说明：配置体是固定 5 字段的平铺对象（3 字符串 + 2 布尔），模块编译期
 * 没有可用的 JSON 库，序列化/解析按该固定形状手写（见 {@link #toJson} 与
 * {@link #jsonString}/{@link #jsonBool}），不受控输入不进这里。
 *
 * @author EasyShare
 */
@Slf4j
@Validated
@SaCheckLogin
@RestController
@RequiredArgsConstructor
@RequestMapping("/easyshare/service")
public class AiConfigController {

    /** AI 配置的 sys_config 键（JSON 值） */
    public static final String AI_CONFIG_KEY = "drive.service.ai.config";
    /** 服务间推送密钥的 sys_config 键 */
    public static final String AI_SECRET_KEY = "drive.service.ai.secret";

    private final SysConfigRowMapper configMapper;

    /** 读取配置（超管）：密钥只回掩码，明文不出控制面。未配置项为空串/null。 */
    @SaCheckRole("superadmin")
    @GetMapping("/ai-config")
    public R<AiConfigVo> config() {
        State state = readState();
        return R.ok(toVo(state, ""));
    }

    /**
     * 保存配置并推送知识服务（超管）。llmApiKey 留空 = 保持已有密钥不变；
     * 推送结果在返回值里（pushed/pushError），保存本身不受推送失败影响。
     */
    @SaCheckRole("superadmin")
    @PutMapping("/ai-config")
    public R<AiConfigVo> save(@Validated @RequestBody SaveBo body) {
        String baseUrl = trim(body.getLlmBaseUrl());
        if (!baseUrl.isEmpty() && !baseUrl.startsWith("http://") && !baseUrl.startsWith("https://")) {
            throw new IllegalArgumentException("LLM 地址必须以 http:// 或 https:// 开头");
        }
        State old = readState();
        String apiKey = trim(body.getLlmApiKey()).isEmpty() ? old.llmApiKey : trim(body.getLlmApiKey());

        State next = new State(baseUrl, trim(body.getLlmModel()), apiKey, body.getAsrEnabled(), body.getAsrSpeakerEnabled());
        upsert(AI_CONFIG_KEY, "AI 能力配置", toJson(next));

        AiConfigVo vo = toVo(next, apiKey);
        PushResult push = pushToKnowledgeService(next);
        vo.setPushed(push.pushed());
        vo.setPushError(push.error());
        return R.ok(vo);
    }

    // --- 推送到知识服务 ---

    private record PushResult(boolean pushed, String error) {
    }

    private PushResult pushToKnowledgeService(State next) {
        String knowledgeUrl = configValue(ServiceConfigController.KNOWLEDGE_URL_KEY);
        String secret = configValue(AI_SECRET_KEY);
        if (knowledgeUrl.isEmpty()) {
            return new PushResult(false, "知识服务地址未登记（服务配置卡片），配置已保存、暂未下发");
        }
        if (secret.isEmpty()) {
            return new PushResult(false, "服务密钥未配置（drive.service.ai.secret），配置已保存、暂未下发");
        }
        try {
            HttpRequest request = HttpRequest.newBuilder()
                .uri(URI.create(knowledgeUrl + "/admin/ai-config"))
                .timeout(Duration.ofSeconds(10))
                .header("Content-Type", "application/json")
                .header("X-Service-Secret", secret)
                .PUT(HttpRequest.BodyPublishers.ofString(toJson(next)))
                .build();
            // 刻意钉死 HTTP/1.1：JDK 客户端默认 HTTP/2 语义，对明文地址会带
            // h2c 升级头（Upgrade: h2c），uvicorn/h11 对此回 422（排障实录见迭代文档）
            HttpResponse<String> response = HttpClient.newBuilder()
                .version(HttpClient.Version.HTTP_1_1)
                .build()
                .send(request, HttpResponse.BodyHandlers.ofString());
            if (response.statusCode() >= 300) {
                log.warn("AI 配置推送失败: status={} body={}", response.statusCode(), response.body());
                return new PushResult(false, "知识服务返回 " + response.statusCode() + "，配置已保存、重启后生效或重新保存重推");
            }
            log.info("AI 配置已推送知识服务: {}", knowledgeUrl);
            return new PushResult(true, "");
        } catch (Exception e) {
            log.warn("AI 配置推送异常: {}", e.getMessage());
            return new PushResult(false, "无法连接知识服务，配置已保存、重启后生效或重新保存重推");
        }
    }

    // --- sys_config 读写（与 ServiceConfigController 同模式） ---

    /** 配置状态：3 字符串 + 2 可空布尔（null=未设置，沿用知识服务 .env） */
    record State(String llmBaseUrl, String llmModel, String llmApiKey, Boolean asrEnabled, Boolean asrSpeakerEnabled) {
    }

    private State readState() {
        String raw = configValue(AI_CONFIG_KEY);
        if (raw.isEmpty()) {
            return new State("", "", "", null, null);
        }
        return new State(
            jsonString(raw, "llmBaseUrl"),
            jsonString(raw, "llmModel"),
            jsonString(raw, "llmApiKey"),
            jsonBool(raw, "asrEnabled"),
            jsonBool(raw, "asrSpeakerEnabled")
        );
    }

    private String configValue(String key) {
        SysConfigRow row = configMapper.selectOne(new LambdaQueryWrapper<SysConfigRow>()
            .eq(SysConfigRow::getConfigKey, key));
        return row == null || row.getConfigValue() == null ? "" : row.getConfigValue().trim();
    }

    private void upsert(String key, String name, String value) {
        SysConfigRow row = configMapper.selectOne(new LambdaQueryWrapper<SysConfigRow>()
            .eq(SysConfigRow::getConfigKey, key));
        if (row == null) {
            row = new SysConfigRow();
            row.setConfigName(name);
            row.setConfigKey(key);
            row.setConfigValue(value);
            row.setConfigType("N");
            row.setRemark("EasyShare AI 能力配置（客户端管理页维护）");
            configMapper.insert(row);
        } else {
            row.setConfigValue(value);
            configMapper.updateById(row);
        }
    }

    private AiConfigVo toVo(State state, String apiKeyPlain) {
        AiConfigVo vo = new AiConfigVo();
        vo.setLlmBaseUrl(state.llmBaseUrl());
        vo.setLlmModel(state.llmModel());
        String apiKey = apiKeyPlain != null ? apiKeyPlain : state.llmApiKey();
        vo.setApiKeySet(!apiKey.isEmpty());
        vo.setApiKeyMasked(apiKey.isEmpty() ? "" : "****" + apiKey.substring(Math.max(0, apiKey.length() - 4)));
        vo.setAsrEnabled(state.asrEnabled());
        vo.setAsrSpeakerEnabled(state.asrSpeakerEnabled());
        return vo;
    }

    // --- 固定形状的极简 JSON 读写 ---

    private static String toJson(State state) {
        return "{"
            + "\"llmBaseUrl\":" + quote(state.llmBaseUrl())
            + ",\"llmModel\":" + quote(state.llmModel())
            + ",\"llmApiKey\":" + quote(state.llmApiKey())
            + ",\"asrEnabled\":" + state.asrEnabled()
            + ",\"asrSpeakerEnabled\":" + state.asrSpeakerEnabled()
            + "}";
    }

    private static String quote(String value) {
        if (value == null) {
            return "null";
        }
        StringBuilder sb = new StringBuilder("\"");
        for (char c : value.toCharArray()) {
            switch (c) {
                case '\\' -> sb.append("\\\\");
                case '"' -> sb.append("\\\"");
                case '\n' -> sb.append("\\n");
                case '\r' -> sb.append("\\r");
                case '\t' -> sb.append("\\t");
                default -> {
                    if (c < 0x20) {
                        sb.append(String.format("\\u%04x", (int) c));
                    } else {
                        sb.append(c);
                    }
                }
            }
        }
        return sb.append('"').toString();
    }

    private static String jsonString(String json, String field) {
        Matcher matcher = Pattern.compile("\"" + field + "\"\\s*:\\s*\"((?:[^\"\\\\]|\\\\.)*)\"").matcher(json);
        if (!matcher.find()) {
            return "";
        }
        return matcher.group(1)
            .replace("\\\"", "\"")
            .replace("\\\\", "\\")
            .replace("\\n", "\n")
            .replace("\\r", "\r")
            .replace("\\t", "\t");
    }

    private static Boolean jsonBool(String json, String field) {
        Matcher matcher = Pattern.compile("\"" + field + "\"\\s*:\\s*(true|false|null)").matcher(json);
        if (!matcher.find()) {
            return null;
        }
        return switch (matcher.group(1)) {
            case "true" -> Boolean.TRUE;
            case "false" -> Boolean.FALSE;
            default -> null;
        };
    }

    private static String trim(String value) {
        return value == null ? "" : value.trim();
    }

    @lombok.Data
    public static class AiConfigVo {
        private String llmBaseUrl;
        private String llmModel;
        private boolean apiKeySet;
        private String apiKeyMasked;
        private Boolean asrEnabled;
        private Boolean asrSpeakerEnabled;
        private boolean pushed;
        private String pushError;
    }

    @lombok.Data
    public static class SaveBo {
        private String llmBaseUrl;
        private String llmModel;
        private String llmApiKey;
        private Boolean asrEnabled;
        private Boolean asrSpeakerEnabled;
    }
}
