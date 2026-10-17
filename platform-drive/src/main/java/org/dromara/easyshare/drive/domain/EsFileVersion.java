package org.dromara.easyshare.drive.domain;

import com.baomidou.mybatisplus.annotation.TableId;
import com.baomidou.mybatisplus.annotation.TableName;
import lombok.Data;

import java.time.LocalDateTime;

/**
 * 文件历史版本。
 *
 * <p>每次覆盖上传前把旧对象复制到 {@link #STORAGE_DIR} 前缀下留档——
 * 客户端直传对象存储，覆盖发生在两次 API 调用之间，控制面只能在
 * presign-put 那一刻抢先复制，否则旧内容就永久没了。
 *
 * @author EasyShare
 */
@Data
@TableName("es_file_version")
public class EsFileVersion {

    /** 版本对象前缀：与正常文件隔离，列表必须跳过它 */
    public static final String STORAGE_DIR = ".versions/";

    @TableId(value = "version_pk")
    private Long versionPk;

    /** 归属文件（es_file.file_id）；改名后历史仍跟着文件走 */
    private Long fileId;

    /** 被覆盖时的版本号（与 es_file.version_id 的历史值对齐） */
    private Long versionId;

    /** personal / shared */
    private String spaceType;

    private Long ownerId;

    private String filePath;

    /** 该版本的字节数快照 */
    private Long fileSize;

    /** 该版本的哈希快照；老数据可能为空 */
    private String contentMd5;

    /** 版本对象的相对路径：.versions/{path}/v{n} */
    private String storagePath;

    private LocalDateTime createdAt;

    /** 触发覆盖上传的用户 */
    private Long createdBy;

    /** 版本对象的相对路径：把路径与版本号编进 key，避免额外查表才能定位 */
    public static String storagePathOf(String path, long versionId) {
        return STORAGE_DIR + path + "/v" + versionId;
    }
}
