package org.dromara.easyshare.drive.domain;

import com.baomidou.mybatisplus.annotation.TableId;
import com.baomidou.mybatisplus.annotation.TableName;
import lombok.Data;

import java.time.LocalDateTime;

/**
 * 用户级文件标记：星标 / 最近访问。
 *
 * <p>挂在「用户 × 空间 × 路径」上，不挂 fileId——同名文件删掉重建会拿到新 fileId，
 * 但用户认知里「我星标的是这个位置的文件」在路径不变时应继续成立。
 *
 * <p>不继承 {@code BaseEntity}：这是一张纯关系表，没有「谁修改了这条标记」的语义，
 * create_by/update_by 在这里只会制造噪音。
 *
 * @author EasyShare
 */
@Data
@TableName("es_file_mark")
public class EsFileMark {

    /** 星标：用户主动标记，长期保留 */
    public static final String KIND_STAR = "star";

    /** 最近访问：由打开/预览/下载触发，按条数与时间裁剪 */
    public static final String KIND_RECENT = "recent";

    @TableId(value = "mark_id")
    private Long markId;

    /** 标记归属用户 */
    private Long userId;

    /** personal / shared */
    private String spaceType;

    /** 空间内相对路径（与 es_file.file_path 同口径） */
    private String filePath;

    /** star / recent */
    private String kind;

    /** 星标为点击时刻，最近为最后一次访问 */
    private LocalDateTime markedAt;
}
