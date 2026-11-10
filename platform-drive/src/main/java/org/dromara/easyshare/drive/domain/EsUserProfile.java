package org.dromara.easyshare.drive.domain;

import com.baomidou.mybatisplus.annotation.TableId;
import com.baomidou.mybatisplus.annotation.TableName;
import lombok.Data;
import lombok.EqualsAndHashCode;
import org.dromara.common.mybatis.core.domain.BaseEntity;

/**
 * 成员个人资料扩展：目前只有头像。
 * <p>
 * 昵称与密码沿用 RuoYi 的 sys_user 与标准个人中心接口；头像单独落这里，
 * 原因是标准头像通道绑定 sys_oss 且要求 system:oss:upload 权限，普通成员用不了。
 *
 * @author EasyShare
 */
@Data
@EqualsAndHashCode(callSuper = true)
@TableName("es_user_profile")
public class EsUserProfile extends BaseEntity {

    /** 账号 ID（sys_user.user_id） */
    @TableId(value = "user_id")
    private Long userId;

    /** 头像 MIME，按文件头嗅探得出 */
    private String avatarMime;

    /** 头像原始字节（≤2MB） */
    private byte[] avatarData;
}
