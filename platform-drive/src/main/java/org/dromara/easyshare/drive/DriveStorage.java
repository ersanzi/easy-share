package org.dromara.easyshare.drive;

import jakarta.annotation.PreDestroy;
import lombok.RequiredArgsConstructor;
import lombok.extern.slf4j.Slf4j;
import org.dromara.common.core.exception.ServiceException;
import org.springframework.stereotype.Component;
import software.amazon.awssdk.auth.credentials.AwsBasicCredentials;
import software.amazon.awssdk.auth.credentials.StaticCredentialsProvider;
import software.amazon.awssdk.core.checksums.RequestChecksumCalculation;
import software.amazon.awssdk.core.checksums.ResponseChecksumValidation;
import software.amazon.awssdk.http.nio.netty.NettyNioAsyncHttpClient;
import software.amazon.awssdk.regions.Region;
import software.amazon.awssdk.services.s3.S3AsyncClient;
import software.amazon.awssdk.services.s3.S3Configuration;
import software.amazon.awssdk.services.s3.model.CompletedMultipartUpload;
import software.amazon.awssdk.services.s3.model.CompletedPart;
import software.amazon.awssdk.services.s3.model.ListObjectsV2Response;
import software.amazon.awssdk.services.s3.model.UploadPartRequest;
import software.amazon.awssdk.services.s3.presigner.S3Presigner;

import java.net.URI;
import java.time.Duration;
import java.util.ArrayList;
import java.util.HashMap;
import java.util.List;
import java.util.Map;

/**
 * RustFS 存储访问层：只有控制面持有真实 AK/SK（ADR-0007 不变量 1，修 KI-2）。
 * <p>
 * 对外只暴露预签名 URL 与列举/删除，客户端拿不到密钥。
 * 所有方法收的都是**相对路径**，绝对键由 {@link DriveKeys} 按登录用户拼出。
 *
 * @author EasyShare
 */
@Slf4j
@Component
@RequiredArgsConstructor
public class DriveStorage {

    private final DriveProperties properties;

    private volatile S3AsyncClient client;
    private volatile S3Presigner presigner;

    /**
     * 惰性初始化 S3 客户端，未配置 RustFS 时不影响控制面其余功能启动。
     */
    private void ensureReady() {
        if (client != null) {
            return;
        }
        synchronized (this) {
            if (client != null) {
                return;
            }
            if (isBlank(properties.getEndpoint()) || isBlank(properties.getAccessKey())
                || isBlank(properties.getSecretKey()) || isBlank(properties.getBucket())) {
                throw new ServiceException("云盘存储未配置，请检查控制面 easyshare.drive.* 配置");
            }
            StaticCredentialsProvider credentials = StaticCredentialsProvider.create(
                AwsBasicCredentials.create(properties.getAccessKey(), properties.getSecretKey()));
            Region region = Region.of(properties.getRegion());
            URI endpoint = URI.create(properties.getEndpoint());

            // forcePathStyle：RustFS 与 MinIO 一样不支持 virtual-host 风格桶名
            // checksum WHEN_REQUIRED：SDK 2.30+ 默认发 CRC32 分块校验，RustFS beta 不认
            this.presigner = S3Presigner.builder()
                .region(region)
                .credentialsProvider(credentials)
                .endpointOverride(endpoint)
                .serviceConfiguration(S3Configuration.builder().pathStyleAccessEnabled(true).build())
                .build();
            this.client = S3AsyncClient.builder()
                .credentialsProvider(credentials)
                .endpointOverride(endpoint)
                .region(region)
                .forcePathStyle(true)
                .requestChecksumCalculation(RequestChecksumCalculation.WHEN_REQUIRED)
                .responseChecksumValidation(ResponseChecksumValidation.WHEN_REQUIRED)
                .httpClient(NettyNioAsyncHttpClient.builder()
                    .connectionTimeout(Duration.ofSeconds(60))
                    .maxConcurrency(64)
                    .build())
                .build();
        }
    }

    /**
     * 按前缀签发上传 URL。刻意不签 Content-Type：客户端可自由带该头而不破坏签名。
     * 前缀由服务端产出，客户端只能发相对路径。
     *
     * @param prefix   空间前缀
     * @param relative 相对路径
     * @return 预签名 PUT URL
     */
    public String presignPutAt(String prefix, String relative) {
        ensureReady();
        String key = DriveKeys.keyAt(prefix, relative);
        return presigner.presignPutObject(b -> b
                .signatureDuration(properties.getPutExpiry())
                .putObjectRequest(r -> r.bucket(properties.getBucket()).key(key)))
            .url().toExternalForm();
    }

    /**
     * 按前缀签发下载 URL。
     *
     * @param prefix   空间前缀
     * @param relative 相对路径
     * @return 预签名 GET URL
     */
    public String presignGetAt(String prefix, String relative) {
        ensureReady();
        String key = DriveKeys.keyAt(prefix, relative);
        return presigner.presignGetObject(b -> b
                .signatureDuration(properties.getGetExpiry())
                .getObjectRequest(r -> r.bucket(properties.getBucket()).key(key)))
            .url().toExternalForm();
    }

    /**
     * 按前缀列举对象。
     *
     * @param prefix 空间前缀
     * @return 对象列表（路径为相对路径）
     */
    public List<DriveObject> listAt(String prefix) {
        ensureReady();
        List<DriveObject> objects = new ArrayList<>();
        eachObject(prefix, (key, size, modified) -> {
            if (!key.endsWith("/")) {
                objects.add(new DriveObject(key.substring(prefix.length()), size, modified));
            }
        });
        return objects;
    }

    /**
     * 创建 Multipart 上传会话（Upload Session 切片，2026-09-06）。
     *
     * @return S3 uploadId
     */
    public String createUpload(String prefix, String relative) {
        ensureReady();
        String key = DriveKeys.keyAt(prefix, relative);
        return join(client.createMultipartUpload(b -> b
                .bucket(properties.getBucket()).key(key)))
            .uploadId();
    }

    /**
     * 签发单分片上传 URL（含 uploadId 与 partNumber 签名）。
     */
    public String presignPartAt(String prefix, String relative, String uploadId, int partNumber) {
        ensureReady();
        String key = DriveKeys.keyAt(prefix, relative);
        return presigner.presignUploadPart(b -> b
                .signatureDuration(properties.getPutExpiry())
                .uploadPartRequest(r -> r.bucket(properties.getBucket()).key(key)
                    .uploadId(uploadId).partNumber(partNumber)))
            .url().toExternalForm();
    }

    /**
     * 提交分片清单完成上传。uploadId 已被消费（重复 Complete/已 Abort）时抛
     * ServiceException——幂等语义由会话表状态在上层保证。
     */
    public void completeUpload(String prefix, String relative, String uploadId,
                               List<UploadPart> parts) {
        ensureReady();
        String key = DriveKeys.keyAt(prefix, relative);
        List<CompletedPart> completed = parts.stream()
            .map(part -> CompletedPart.builder()
                .partNumber(part.partNumber()).eTag(part.etag()).build())
            .toList();
        join(client.completeMultipartUpload(b -> b
            .bucket(properties.getBucket()).key(key).uploadId(uploadId)
            .multipartUpload(CompletedMultipartUpload.builder()
                .parts(completed).build())));
    }

    /**
     * 放弃 Multipart 会话并清理 S3 端已传分片。uploadId 已不存在时静默成功
     * （Abort 本就是清理语义）。
     */
    public void abortUpload(String prefix, String relative, String uploadId) {
        ensureReady();
        String key = DriveKeys.keyAt(prefix, relative);
        try {
            join(client.abortMultipartUpload(b -> b
                .bucket(properties.getBucket()).key(key).uploadId(uploadId)));
        } catch (ServiceException ignored) {
            // S3 端已无此 uploadId（重复 Abort / 已 Complete）：清理语义视作成功
        }
    }

    /**
     * 上传分片回执：分片号与 S3 返回的 ETag（Complete 的最小必填项）。
     */
    public record UploadPart(int partNumber, String etag) {
    }

    /**
     * 按前缀删除对象。
     *
     * @param prefix   空间前缀
     * @param relative 相对路径
     */
    public void deleteAt(String prefix, String relative) {
        ensureReady();
        String key = DriveKeys.keyAt(prefix, relative);
        join(client.deleteObject(b -> b.bucket(properties.getBucket()).key(key)));
    }

    /**
     * 汇总某前缀下所有对象的字节数。配额校验的用量来源——真值只在这里，不在库里。
     *
     * @param prefix 空间前缀
     * @return 已用字节数
     */
    public long sumBytes(String prefix) {
        ensureReady();
        long[] total = {0L};
        eachObject(prefix, (key, size, modified) -> total[0] += size);
        return total[0];
    }

    /**
     * 单次全桶遍历，按空间前缀分组汇总字节数。
     * <p>
     * 管理页要显示每个账号的已用量，逐账号 list 会是 N 次请求；这里一次列完按前缀分组，
     * 只发一次（翻页除外）。
     *
     * @return 空间前缀 → 已用字节数
     */
    public Map<String, Long> sumBytesGrouped() {
        ensureReady();
        Map<String, Long> grouped = new HashMap<>();
        eachObject("", (key, size, modified) -> {
            String prefix = spacePrefixOf(key);
            if (prefix != null) {
                grouped.merge(prefix, size, Long::sum);
            }
        });
        return grouped;
    }

    /**
     * 从对象键反推它属于哪个空间前缀，不属于任何已知空间返回 null。
     */
    private static String spacePrefixOf(String key) {
        if (key.startsWith(DriveKeys.sharedPrefix())) {
            return DriveKeys.sharedPrefix();
        }
        if (key.startsWith("users/")) {
            int slash = key.indexOf('/', "users/".length());
            if (slash > 0) {
                return key.substring(0, slash + 1);
            }
        }
        return null;
    }

    /**
     * 翻页遍历某前缀下的对象，屏蔽 continuation token 细节。
     */
    private void eachObject(String prefix, ObjectVisitor visitor) {
        String token = null;
        do {
            final String continuation = token;
            ListObjectsV2Response response = join(client.listObjectsV2(b -> b
                .bucket(properties.getBucket())
                .prefix(prefix)
                .maxKeys(1000)
                .continuationToken(continuation)));
            response.contents().forEach(o -> visitor.visit(o.key(), o.size(), o.lastModified()));
            token = Boolean.TRUE.equals(response.isTruncated()) ? response.nextContinuationToken() : null;
        } while (token != null);
    }

    /**
     * 对象访问回调。
     */
    private interface ObjectVisitor {
        void visit(String key, long size, java.time.Instant lastModified);
    }

    /**
     * 阻塞等待异步结果，并把 SDK 的 CompletionException 展开为可读错误。
     */
    private static <T> T join(java.util.concurrent.CompletableFuture<T> future) {
        try {
            return future.join();
        } catch (java.util.concurrent.CompletionException e) {
            Throwable cause = e.getCause() == null ? e : e.getCause();
            throw new ServiceException("对象存储访问失败: " + cause.getMessage());
        }
    }

    private static boolean isBlank(String value) {
        return value == null || value.isBlank();
    }

    @PreDestroy
    public void close() {
        if (presigner != null) {
            presigner.close();
        }
        if (client != null) {
            client.close();
        }
    }
}
