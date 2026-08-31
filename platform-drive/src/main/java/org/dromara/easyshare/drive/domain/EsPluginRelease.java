package org.dromara.easyshare.drive.domain;

import com.baomidou.mybatisplus.annotation.TableId;
import com.baomidou.mybatisplus.annotation.TableName;
import lombok.Data;
import lombok.EqualsAndHashCode;
import org.dromara.common.mybatis.core.domain.BaseEntity;

/**
 * 插件发布版本实体。同插件同版本唯一（重传=覆盖发布）。
 *
 * @author EasyShare
 */
@Data
@EqualsAndHashCode(callSuper = true)
@TableName("es_plugin_release")
public class EsPluginRelease extends BaseEntity {

    @TableId(value = "release_id")
    private Long releaseId;

    /** 所属插件 ID */
    private String pluginId;

    /** 版本号，如 1.0.0 */
    private String version;

    /** 更新说明 */
    private String notes;
}
