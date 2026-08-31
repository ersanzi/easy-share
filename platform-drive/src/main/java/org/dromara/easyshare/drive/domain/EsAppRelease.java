package org.dromara.easyshare.drive.domain;

import com.baomidou.mybatisplus.annotation.TableId;
import com.baomidou.mybatisplus.annotation.TableName;
import lombok.Data;
import lombok.EqualsAndHashCode;
import org.dromara.common.mybatis.core.domain.BaseEntity;

/**
 * 发布版本实体：一行一个版本。
 * <p>
 * version 唯一——重传同版本是「覆盖发布」而非新版本；latest 清单按首次发布时间取最新。
 *
 * @author EasyShare
 */
@Data
@EqualsAndHashCode(callSuper = true)
@TableName("es_app_release")
public class EsAppRelease extends BaseEntity {

    @TableId(value = "release_id")
    private Long releaseId;

    /** 版本号，如 0.1.0 / 0.1.0-preview.1 */
    private String version;

    /** 更新说明，多行纯文本 */
    private String notes;
}
