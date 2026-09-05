package org.dromara.easyshare.drive;

import lombok.Data;
import org.springframework.boot.context.properties.ConfigurationProperties;
import org.springframework.stereotype.Component;
import org.springframework.util.unit.DataSize;

import java.time.Duration;

/**
 * EasyShare 云盘存储配置。
 * <p>
 * 这些是 RustFS 的真实凭据，按 ADR-0007 不变量 1 只允许存在于控制面，
 * 绝不下发客户端。配置文件见仓库 deploy/ruoyi-db/easyshare-drive.yml。
 *
 * @author EasyShare
 */
@Data
@Component
@ConfigurationProperties(prefix = "easyshare.drive")
public class DriveProperties {

    /**
     * RustFS 服务地址，如 http://127.0.0.1:9000
     */
    private String endpoint;

    /**
     * 区域，RustFS 无实际意义但 SigV4 签名必需
     */
    private String region = "us-east-1";

    /**
     * 访问密钥
     */
    private String accessKey;

    /**
     * 密钥
     */
    private String secretKey;

    /**
     * 存储桶
     */
    private String bucket;

    /**
     * 上传预签名 URL 有效期
     */
    private Duration putExpiry = Duration.ofMinutes(15);

    /**
     * 下载预签名 URL 有效期
     */
    private Duration getExpiry = Duration.ofMinutes(10);

    /**
     * 用于探测物理可用容量的路径，留空则不启用池上限。
     * <p>
     * 必须指向**对象存储数据实际落盘的那个卷**。RustFS 跑在容器里，容器内 {@code df}
     * 看到的是 WSL2 稀疏 vhdx 的虚数（可能显示 1 TB 而宿主只剩几十 GB），不可作为依据；
     * 所以这里要的是宿主侧路径，由部署方按实际情况配置——vhdx 的位置取决于
     * Docker Desktop 的设置，猜不出来。
     */
    private String capacityPath;

    /**
     * 预留水位：探测到的可用容量中不允许云盘使用的部分。
     * <p>
     * 磁盘写满会连带影响 PostgreSQL、Redis 与系统本身，不能让云盘吃到 0。
     */
    private DataSize reservedBytes = DataSize.ofGigabytes(5);

    /**
     * Multipart 分片大小（Upload Session 切片用）。技术参数由服务端定，客户端不选——
     * S3 上限 10000 片，8MB 分片支持到 80GB 单文件，观察期场景绰绰有余。
     */
    private DataSize partSize = DataSize.ofMegabytes(8);
}
