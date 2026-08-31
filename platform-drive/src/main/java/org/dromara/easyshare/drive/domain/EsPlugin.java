package org.dromara.easyshare.drive.domain;

import com.baomidou.mybatisplus.annotation.TableId;
import com.baomidou.mybatisplus.annotation.TableName;
import lombok.Data;
import lombok.EqualsAndHashCode;
import org.dromara.common.mybatis.core.domain.BaseEntity;

/**
 * 插件登记实体：商城的一个条目。
 * <p>
 * plugin_id 即客户端 manifest 的 id（稳定不变），版本与资产挂在
 * {@link EsPluginRelease} / {@link EsPluginReleaseAsset} 下。
 *
 * @author EasyShare
 */
@Data
@EqualsAndHashCode(callSuper = true)
@TableName("es_plugin")
public class EsPlugin extends BaseEntity {

    /** 插件 ID（小写字母开头，字母/数字/连字符，2~32 位） */
    @TableId(value = "plugin_id")
    private String pluginId;

    /** 插件显示名 */
    private String name;

    /** 插件说明 */
    private String description;

    /** emoji 或包内图标文件相对路径 */
    private String icon;

    /** 作者 */
    private String author;
}
