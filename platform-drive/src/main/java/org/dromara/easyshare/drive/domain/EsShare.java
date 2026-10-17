package org.dromara.easyshare.drive.domain;

import com.baomidou.mybatisplus.annotation.TableId;
import com.baomidou.mybatisplus.annotation.TableName;
import lombok.Data;

import java.time.LocalDateTime;

/**
 * 文件分享记录：一条记录对应一个可撤销的外链。
 *
 * <p>{@code fileName}/{@code fileSize} 是**创建时的快照**：文件后来被删或改名，
 * 「我的分享」列表仍要能显示这行是什么，不能因为 join 不到就变成空白。
 *
 * @author EasyShare
 */
@Data
@TableName("es_share")
public class EsShare {

    @TableId(value = "share_id")
    private Long shareId;

    /** URL 中的随机码（base62 22 位）；不可枚举，泄露单条不影响其他分享 */
    private String token;

    private Long ownerId;

    /** personal / shared */
    private String spaceType;

    /** 分享时文件的空间内相对路径 */
    private String filePath;

    /** 创建时的文件名快照 */
    private String fileName;

    /** 创建时的字节数快照 */
    private Long fileSize;

    /** 到期时间 */
    private LocalDateTime expiresAt;

    /** 是否已主动撤销；撤销立即生效，不等过期 */
    private Boolean revoked;

    /** 成功下载次数（302 跳转计数，不是精确审计） */
    private Integer visitCount;

    private LocalDateTime createdAt;

    /** 仍可用 = 未撤销 且 未过期 */
    public boolean usableAt(LocalDateTime now) {
        return !Boolean.TRUE.equals(revoked)
            && expiresAt != null
            && expiresAt.isAfter(now);
    }
}
